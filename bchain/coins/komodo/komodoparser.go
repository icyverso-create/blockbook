package komodo

import (
	"github.com/martinboehm/btcd/wire"
	"github.com/martinboehm/btcutil/chaincfg"
	"github.com/trezor/blockbook/bchain"
	"github.com/trezor/blockbook/bchain/coins/btc"
)

// Network magics, taken from pchMessageStart in Komodo's src/chainparams.cpp
// and read as a little-endian uint32 (the same convention the other coins use).
const (
	// MainnetMagic is mainnet network constant
	MainnetMagic wire.BitcoinNet = 0x8de4eef9
	// TestnetMagic is testnet network constant
	TestnetMagic wire.BitcoinNet = 0x627e1f5a
	// RegtestMagic is regtest network constant
	RegtestMagic wire.BitcoinNet = 0xf5f38eaa
)

var (
	// MainNetParams are parser parameters for mainnet
	MainNetParams chaincfg.Params
	// TestNetParams are parser parameters for testnet
	TestNetParams chaincfg.Params
	// RegtestParams are parser parameters for regtest
	RegtestParams chaincfg.Params
)

func init() {
	MainNetParams = chaincfg.MainNetParams
	MainNetParams.Net = MainnetMagic

	// Address encoding magics.
	// Unlike Zcash, from which Komodo is derived, the base58 prefixes are a
	// single byte (base58Prefixes in src/chainparams.cpp).
	MainNetParams.AddressMagicLen = 1
	MainNetParams.PubKeyHashAddrID = []byte{60} // base58 prefix: R
	MainNetParams.ScriptHashAddrID = []byte{85} // base58 prefix: b

	TestNetParams = chaincfg.TestNet3Params
	TestNetParams.Net = TestnetMagic

	// Address encoding magics
	TestNetParams.AddressMagicLen = 1
	TestNetParams.PubKeyHashAddrID = []byte{0}
	TestNetParams.ScriptHashAddrID = []byte{5}

	RegtestParams = chaincfg.RegressionNetParams
	RegtestParams.Net = RegtestMagic

	// Address encoding magics
	RegtestParams.AddressMagicLen = 1
	RegtestParams.PubKeyHashAddrID = []byte{60}
	RegtestParams.ScriptHashAddrID = []byte{85}
}

// KomodoParser handle
type KomodoParser struct {
	*btc.BitcoinLikeParser
	baseparser *bchain.BaseParser
}

// NewKomodoParser returns new KomodoParser instance
func NewKomodoParser(params *chaincfg.Params, c *btc.Configuration) *KomodoParser {
	return &KomodoParser{
		BitcoinLikeParser: btc.NewBitcoinLikeParser(params, c),
		baseparser:        &bchain.BaseParser{},
	}
}

// GetChainParams contains network parameters for the main Komodo network,
// the regression test Komodo network and the test Komodo network, in this order
func GetChainParams(chain string) *chaincfg.Params {
	if !chaincfg.IsRegistered(&MainNetParams) {
		err := chaincfg.Register(&MainNetParams)
		if err == nil {
			err = chaincfg.Register(&TestNetParams)
		}
		if err == nil {
			err = chaincfg.Register(&RegtestParams)
		}
		if err != nil {
			panic(err)
		}
	}
	switch chain {
	case "test":
		return &TestNetParams
	case "regtest":
		return &RegtestParams
	default:
		return &MainNetParams
	}
}

// PackTx packs transaction to byte array using protobuf.
// Komodo transactions are Overwinter/Sapling shaped (versiongroupid, vShieldedSpend,
// vShieldedOutput, vjoinsplit), which the btcd wire format reader cannot decode,
// so transactions are stored via the generic protobuf serialization instead of raw hex.
func (p *KomodoParser) PackTx(tx *bchain.Tx, height uint32, blockTime int64) ([]byte, error) {
	return p.baseparser.PackTx(tx, height, blockTime)
}

// UnpackTx unpacks transaction from protobuf byte array
func (p *KomodoParser) UnpackTx(buf []byte) (*bchain.Tx, uint32, error) {
	return p.baseparser.UnpackTx(buf)
}
