package bsv

import (
	"encoding/json"
	"math/big"

	"github.com/golang/glog"
	"github.com/juju/errors"
	"github.com/trezor/blockbook/bchain"
	"github.com/trezor/blockbook/bchain/coins/btc"
)

// BSVRPC is an interface to JSON-RPC service of a Bitcoin SV node.
//
// Bitcoin SV is a fork of Bitcoin, not of Bitcoin Cash as its historical
// "Bcash SV" naming suggests: it uses plain base58 addresses and never adopted
// CashAddr, so it must not be served by the bch connector, whose parser encodes
// every address as CashAddr unless address_format is set to "legacy".
//
// The JSON-RPC surface blockbook needs is the Bitcoin Core one, so
// btc.BitcoinRPC is used unchanged:
//   - getblock takes a numeric verbosity (src/rpc/blockchain.cpp:3779,
//     argNames {"blockhash","verbosity|verbose"}), 0 gives the raw hex block;
//   - getblockheader accepts the name "verbose" as an alias of "verbosity"
//     (src/rpc/blockchain.cpp:3782);
//   - named JSON-RPC arguments are supported (transformNamedArguments,
//     src/rpc/server.cpp:434), so the default JSONMarshalerV2 is fine;
//   - the "Block not found" / "Block height out of range" error strings
//     btc.IsErrBlockNotFound matches are the ones the node produces
//     (src/rpc/blockchain.cpp:819, 979, 1256).
type BSVRPC struct {
	*btc.BitcoinRPC
}

// ErrFeeEstimationNotSupported is returned instead of calling the backend for a
// fee estimate. A Bitcoin SV node implements neither estimatefee nor
// estimatesmartfee - both answer "Method not found" - and there is no cheap
// substitute that would not be an invented number, so the calls are refused
// locally rather than turned into a guaranteed-failing round trip to the node
// (api.Worker.getTxETA probes the estimator up to eight times per rendered
// mempool transaction and does not cache failures).
var ErrFeeEstimationNotSupported = errors.New("fee estimation is not supported by the Bitcoin SV backend")

// NewBSVRPC returns new BSVRPC instance.
func NewBSVRPC(config json.RawMessage, pushHandler func(bchain.NotificationType)) (bchain.BlockChain, error) {
	b, err := btc.NewBitcoinRPC(config, pushHandler)
	if err != nil {
		return nil, err
	}

	s := &BSVRPC{
		b.(*btc.BitcoinRPC),
	}
	// btc.NewBitcoinRPC optimistically enables both estimators; neither RPC
	// exists on a Bitcoin SV node. Keeping the flags truthful also stops
	// btc.BitcoinRPC from silently substituting one estimator for the other.
	s.ChainConfig.SupportsEstimateFee = false
	s.ChainConfig.SupportsEstimateSmartFee = false

	return s, nil
}

// Initialize initializes BSVRPC instance.
func (b *BSVRPC) Initialize() error {
	ci, err := b.GetChainInfo()
	if err != nil {
		return err
	}
	chainName := ci.Chain

	// btc.BitcoinRPC.Initialize is deliberately not delegated to: it would
	// install the Bitcoin parser and reset SupportsEstimateFee on its own.
	b.ChainConfig.SupportsEstimateFee = false
	b.ChainConfig.SupportsEstimateSmartFee = false

	params := GetChainParams(chainName)

	// always create parser
	b.Parser, err = NewBSVParser(params, b.ChainConfig)
	if err != nil {
		return err
	}

	// parameters for getInfo request
	if params.Net == MainnetMagic {
		b.Testnet = false
		b.Network = "livenet"
	} else {
		b.Testnet = true
		b.Network = "testnet"
	}

	glog.Info("rpc: block chain ", params.Name)

	return nil
}

// EstimateFee returns fee estimation. Not supported by Bitcoin SV.
func (b *BSVRPC) EstimateFee(blocks int) (big.Int, error) {
	var r big.Int
	return r, ErrFeeEstimationNotSupported
}

// EstimateSmartFee returns fee estimation. Not supported by Bitcoin SV.
func (b *BSVRPC) EstimateSmartFee(blocks int, conservative bool) (big.Int, error) {
	var r big.Int
	return r, ErrFeeEstimationNotSupported
}

// LongTermFeeRate returns the smallest fee rate from historic blocks. It is
// derived from estimatesmartfee and is therefore unavailable on Bitcoin SV.
func (b *BSVRPC) LongTermFeeRate() (*bchain.LongTermFeeRate, error) {
	return nil, ErrFeeEstimationNotSupported
}
