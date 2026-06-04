package eth

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/golang/glog"
	"github.com/trezor/blockbook/bchain"
)

// parityTraceAction is the inner "action" object of a Parity-style trace entry.
// Different fields are populated depending on the entry's outer type:
//   - call:    CallType, From, To, Value, Gas, Input
//   - create:  From, Value, Gas, Init
//   - suicide: Address (self-destructing contract), RefundAddress, Balance
type parityTraceAction struct {
	CallType      string `json:"callType,omitempty"`
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
	Value         string `json:"value,omitempty"`
	Gas           string `json:"gas,omitempty"`
	Input         string `json:"input,omitempty"`
	Init          string `json:"init,omitempty"`
	Address       string `json:"address,omitempty"`
	RefundAddress string `json:"refundAddress,omitempty"`
	Balance       string `json:"balance,omitempty"`
}

// parityTraceResult is the "result" object. May be nil/absent on failed traces.
//   - call:   GasUsed, Output
//   - create: GasUsed, Address (deployed contract), Code
type parityTraceResult struct {
	GasUsed string `json:"gasUsed,omitempty"`
	Output  string `json:"output,omitempty"`
	Address string `json:"address,omitempty"`
	Code    string `json:"code,omitempty"`
}

// parityTraceEntry mirrors one element of the flat array returned by
// trace_block (Parity / OpenEthereum / arb-classic when launched with
// --node.rpc.tracing.namespace=trace).
type parityTraceEntry struct {
	Action              parityTraceAction  `json:"action"`
	Result              *parityTraceResult `json:"result,omitempty"`
	Subtraces           int                `json:"subtraces"`
	TraceAddress        []int              `json:"traceAddress"`
	TransactionHash     string             `json:"transactionHash"`
	TransactionPosition int                `json:"transactionPosition"`
	Type                string             `json:"type"` // "call" | "create" | "suicide" | "reward"
	Error               string             `json:"error,omitempty"`
}

// getClassicEraInternalData fetches internal transactions for a pre-migration
// (classic-era) block via Parity-style trace_block on b.ClassicTraceRPC and
// converts the flat trace array into the same []EthereumInternalData /
// []ContractInfo shape produced by debug_traceBlockByHash + callTracer.
//
// The returned data slice is positionally aligned with `transactions` (one
// EthereumInternalData per RpcTransaction at the same index) so the rest of
// the indexer pipeline can store it identically to nitro-era blocks.
func (b *EthereumRPC) getClassicEraInternalData(ctx context.Context, blockHeight uint32, transactions []bchain.RpcTransaction) ([]bchain.EthereumInternalData, []bchain.ContractInfo, error) {
	data := make([]bchain.EthereumInternalData, len(transactions))
	contracts := make([]bchain.ContractInfo, 0)

	// Skip the trace round-trip on empty blocks. Classic arb-node has to
	// re-execute state from the previous checkpoint for trace_block calls; on
	// the vast majority of pre-Nitro blocks the result is just an empty array
	// anyway. With 8+ concurrent workers this short-circuit alone cuts
	// classic-era sync wall-clock by ~10-100x because the long stretches of
	// zero-tx blocks at the start of the chain stop pinning the classic node.
	if len(transactions) == 0 {
		return data, contracts, nil
	}

	// trace_block accepts a quantity (block number) as the single param.
	blockParam := "0x" + strconv.FormatUint(uint64(blockHeight), 16)

	var trace []parityTraceEntry
	err := b.ClassicTraceRPC.CallContext(ctx, &trace, "trace_block", blockParam)
	b.observeEthSyncRpcError("trace_block", err)
	if err != nil {
		glog.Error("trace_block block ", blockHeight, ", error ", err)
		return data, contracts, err
	}

	// Index transactions by hash so we can map flat trace entries back to their
	// transaction's slot in the result slice. Hashes are stored as 0x-prefixed
	// lowercase strings in bchain.RpcTransaction; trace_block returns them the
	// same way.
	txIdx := make(map[string]int, len(transactions))
	for i := range transactions {
		txIdx[strings.ToLower(transactions[i].Hash)] = i
	}

	for _, entry := range trace {
		// Reward entries (miner/uncle payouts) have no transactionHash; skip.
		if entry.TransactionHash == "" {
			continue
		}
		i, ok := txIdx[strings.ToLower(entry.TransactionHash)]
		if !ok {
			// Trace references a tx not in this block — should never happen.
			glog.Warning("trace_block block ", blockHeight, ", trace tx not found in block: ", entry.TransactionHash)
			continue
		}
		d := &data[i]

		// On a per-tx root entry (traceAddress empty) we set the top-level
		// Type/Contract field — same way upstream does for the root of geth's
		// nested callTracer output.
		isRoot := len(entry.TraceAddress) == 0

		switch entry.Type {
		case "create", "create2":
			contract := ""
			if entry.Result != nil && entry.Result.Address != "" {
				contract = entry.Result.Address
			}
			if isRoot {
				d.Type = bchain.CREATE
				d.Contract = contract
				if contract != "" {
					contracts = append(contracts, *b.getCreationContractInfo(contract, blockHeight))
				}
			} else if contract != "" {
				contracts = append(contracts, *b.getCreationContractInfo(contract, blockHeight))
			}
			value := parseHexBig(entry.Action.Value)
			if value.BitLen() > 0 || b.ChainConfig.ProcessZeroInternalTransactions {
				d.Transfers = append(d.Transfers, bchain.EthereumInternalTransfer{
					Type:  bchain.CREATE,
					Value: *value,
					From:  entry.Action.From,
					To:    contract,
				})
			}

		case "suicide":
			value := parseHexBig(entry.Action.Balance)
			if isRoot {
				d.Type = bchain.SELFDESTRUCT
				contracts = append(contracts, bchain.ContractInfo{
					Contract:          entry.Action.Address,
					DestructedInBlock: blockHeight,
				})
			} else {
				contracts = append(contracts, bchain.ContractInfo{
					Contract:          entry.Action.Address,
					DestructedInBlock: blockHeight,
				})
			}
			d.Transfers = append(d.Transfers, bchain.EthereumInternalTransfer{
				Type:  bchain.SELFDESTRUCT,
				Value: *value,
				From:  entry.Action.Address,
				To:    entry.Action.RefundAddress,
			})

		case "call":
			// Parity exposes the call sub-type via action.callType.
			// Upstream's callTracer-based path skips DELEGATECALL; we mirror it.
			if strings.EqualFold(entry.Action.CallType, "delegatecall") {
				continue
			}
			value := parseHexBig(entry.Action.Value)
			if value.BitLen() > 0 || b.ChainConfig.ProcessZeroInternalTransactions {
				d.Transfers = append(d.Transfers, bchain.EthereumInternalTransfer{
					Value: *value,
					From:  entry.Action.From,
					To:    entry.Action.To,
				})
			}

		// "reward" entries were filtered above by empty TransactionHash.
		default:
			// Unknown future Parity trace type — log once-per-block and skip.
			glog.V(2).Info("trace_block block ", blockHeight, ", unhandled trace type ", entry.Type)
		}

		if entry.Error != "" {
			// Mirror upstream behaviour: prefer a packed error short-form for the root,
			// preserve nested errors verbatim. This matches what processCallTrace does
			// for nested calls in the geth path.
			baseError := PackInternalTransactionError(entry.Error)
			if len(baseError) > 1 {
				baseError = strings.ToUpper(baseError[:1]) + baseError[1:] + ". "
			}
			if d.Error == "" {
				d.Error = baseError
			} else {
				d.Error = baseError + d.Error
			}
		}
	}

	if glog.V(2) {
		glog.Info(fmt.Sprintf("classic trace_block %d: %d trace entries, %d txs", blockHeight, len(trace), len(transactions)))
	}
	return data, contracts, nil
}

// parseHexBig decodes a 0x-prefixed hex integer. Returns big.Int(0) on any
// parse error or empty input — internal value fields are routinely empty for
// create/suicide actions.
func parseHexBig(s string) *big.Int {
	if s == "" || s == "0x" || s == "0x0" {
		return new(big.Int)
	}
	v, err := hexutil.DecodeBig(s)
	if err != nil {
		return new(big.Int)
	}
	return v
}

