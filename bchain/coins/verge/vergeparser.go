package verge

import (
	"github.com/martinboehm/btcd/wire"
	"github.com/martinboehm/btcutil/chaincfg"
	"github.com/trezor/blockbook/bchain"
	"github.com/trezor/blockbook/bchain/coins/btc"
)

const (
	MainnetMagic wire.BitcoinNet = 0xff7ea7f7
)

var (
	MainNetParams chaincfg.Params
)

func init() {
	MainNetParams = chaincfg.MainNetParams
	MainNetParams.Net = MainnetMagic
	MainNetParams.PubKeyHashAddrID = []byte{30}
	MainNetParams.ScriptHashAddrID = []byte{33}
	MainNetParams.Bech32HRPSegwit = "vg"
}

// VergeParser handle
type VergeParser struct {
	*btc.BitcoinLikeParser
}

// VergeAmountDecimalPoint defines number of decimal points in Verge amounts
const VergeAmountDecimalPoint = 6

// NewVergeParser returns new VergeParser instance
func NewVergeParser(params *chaincfg.Params, c *btc.Configuration) *VergeParser {
	p := &VergeParser{
		BitcoinLikeParser: btc.NewBitcoinLikeParser(params, c),
	}
	p.AmountDecimalPoint = VergeAmountDecimalPoint
	return p
}

// PackTx packs transaction to byte array using protobuf
// Verge has non-standard wire format (nTime field in transactions),
// so we use protobuf serialization instead of raw hex storage
func (p *VergeParser) PackTx(tx *bchain.Tx, height uint32, blockTime int64) ([]byte, error) {
	return p.BaseParser.PackTx(tx, height, blockTime)
}

// UnpackTx unpacks transaction from protobuf byte array
func (p *VergeParser) UnpackTx(buf []byte) (*bchain.Tx, uint32, error) {
	return p.BaseParser.UnpackTx(buf)
}

// GetChainParams contains network parameters for the main Verge network
func GetChainParams(chain string) *chaincfg.Params {
	if !chaincfg.IsRegistered(&MainNetParams) {
		err := chaincfg.Register(&MainNetParams)
		if err != nil {
			panic(err)
		}
	}
	return &MainNetParams
}
