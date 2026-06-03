package arbitrum

import (
	"context"
	"encoding/json"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/golang/glog"
	"github.com/juju/errors"
	"github.com/trezor/blockbook/bchain"
	"github.com/trezor/blockbook/bchain/coins/eth"
)

const (
	ArbitrumOneMainNet  eth.Network = 42161
	ArbitrumNovaMainNet eth.Network = 42170
)

// ArbitrumRPC is an interface to JSON-RPC arbitrum service.
type ArbitrumRPC struct {
	*eth.EthereumRPC
}

// NewArbitrumRPC returns new ArbitrumRPC instance.
func NewArbitrumRPC(config json.RawMessage, pushHandler func(bchain.NotificationType)) (bchain.BlockChain, error) {
	c, err := eth.NewEthereumRPC(config, pushHandler)
	if err != nil {
		return nil, err
	}

	s := &ArbitrumRPC{
		EthereumRPC: c.(*eth.EthereumRPC),
	}

	return s, nil
}

// Initialize arbitrum rpc interface
func (b *ArbitrumRPC) Initialize() error {
	b.OpenRPC = eth.OpenRPC

	rc, ec, err := b.OpenRPC(b.ChainConfig.RPCURL, b.ChainConfig.RPCURLWS)
	if err != nil {
		return err
	}

	// set chain specific
	b.Client = ec
	b.RPC = rc
	b.NewBlock = eth.NewEthereumNewBlock()
	b.NewTx = eth.NewEthereumNewTx()

	ctx, cancel := context.WithTimeout(context.Background(), b.Timeout)
	defer cancel()

	id, err := b.Client.NetworkID(ctx)
	if err != nil {
		return err
	}

	// parameters for getInfo request
	switch eth.Network(id.Uint64()) {
	case ArbitrumOneMainNet:
		b.MainNetChainID = ArbitrumOneMainNet
		b.Testnet = false
		b.Network = "livenet"
	case ArbitrumNovaMainNet:
		b.MainNetChainID = ArbitrumNovaMainNet
		b.Testnet = false
		b.Network = "livenet"
	default:
		return errors.Errorf("Unknown network id %v", id)
	}

	if err = b.InitAlternativeProviders(); err != nil {
		return err
	}

	// Wire up an optional Parity-trace JSON-RPC client used for blocks at or
	// below ChainConfig.ClassicTraceCutoffBlock. On Arbitrum One the pre-Nitro
	// chain (blocks <= 22207816) is served by a separate arb-classic node that
	// exposes trace_block but not debug_traceBlockByHash. When configured, the
	// upstream eth.EthereumRPC.getInternalDataForBlock prefers this client for
	// classic-era blocks and falls back to the standard debug_trace path for
	// nitro-era blocks (one indexer covers the whole chain transparently).
	if b.ChainConfig.ClassicTraceRPCURL != "" {
		// Classic tracing is request/response only — no subscriptions — so we
		// dial directly instead of going through eth.OpenRPC (which requires a
		// matching ws URL). The lightweight EthereumRPCClient wrapper is enough
		// to satisfy bchain.EVMRPCClient for CallContext/BatchCallContext.
		dialCtx, dialCancel := context.WithTimeout(context.Background(), b.Timeout)
		classicConn, cerr := rpc.DialContext(dialCtx, b.ChainConfig.ClassicTraceRPCURL)
		dialCancel()
		if cerr != nil {
			return errors.Annotatef(cerr, "classic trace rpc %s", b.ChainConfig.ClassicTraceRPCURL)
		}
		b.ClassicTraceRPC = &eth.EthereumRPCClient{Client: classicConn}
		glog.Infof("rpc: classic trace fallback armed url=%s cutoffBlock=%d",
			b.ChainConfig.ClassicTraceRPCURL, b.ChainConfig.ClassicTraceCutoffBlock)
	}

	glog.Info("rpc: block chain ", b.Network)

	return nil
}

func (b *ArbitrumRPC) ResolveENS(name string) (*bchain.ENSResolution, error) {
	return b.EthereumRPC.ResolveENS(name)
}
