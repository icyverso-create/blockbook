package bsv

import (
	"fmt"

	"github.com/martinboehm/btcd/wire"
	"github.com/martinboehm/btcutil/chaincfg"
	"github.com/trezor/blockbook/bchain/coins/btc"
)

// Network magics of Bitcoin SV.
//
// Source: bitcoin-sv-1.2.1/src/chainparams.cpp, the `netMagic` byte arrays.
// The four bytes are read as a little-endian uint32, which is the convention
// btcd uses for wire.BitcoinNet (Bitcoin's pchMessageStart f9 be b4 d9 is
// wire.MainNet == 0xd9b4bef9).
//
// Bitcoin SV never changed the netMagic it inherited from Bitcoin Cash, so the
// mainnet/testnet/regtest values are numerically equal to the ones in
// martinboehm/bchutil. They are declared here instead of imported so that this
// package does not depend on the CashAddr library, which Bitcoin SV has no use
// for (there is no cashaddr implementation in the bitcoin-sv sources at all).
const (
	// MainnetMagic is netMagic {0xe3, 0xe1, 0xf3, 0xe8} - src/chainparams.cpp:153-156
	MainnetMagic wire.BitcoinNet = 0xe8f3e1e3
	// TestnetMagic is netMagic {0xf4, 0xe5, 0xf3, 0xf4} - src/chainparams.cpp:425-428
	TestnetMagic wire.BitcoinNet = 0xf4f3e5f4
	// StnMagic is the scaling test network, netMagic {0xfb, 0xce, 0xc4, 0xf9} - src/chainparams.cpp:314-317
	StnMagic wire.BitcoinNet = 0xf9c4cefb
	// RegtestMagic is netMagic {0xda, 0xb5, 0xbf, 0xfa} - src/chainparams.cpp:552-555
	RegtestMagic wire.BitcoinNet = 0xfabfb5da
)

var (
	// MainNetParams are parser parameters for mainnet
	MainNetParams chaincfg.Params
	// TestNetParams are parser parameters for testnet
	TestNetParams chaincfg.Params
	// StnParams are parser parameters for the scaling test network
	StnParams chaincfg.Params
	// RegtestParams are parser parameters for regtest
	RegtestParams chaincfg.Params
)

func init() {
	// Bitcoin SV is a fork of Bitcoin and, unlike Bitcoin Cash, it kept the
	// plain base58 address encoding - the whole address layer is byte for byte
	// the one of Bitcoin. The base58 prefixes below are therefore identical to
	// chaincfg.MainNetParams; they are assigned explicitly so that a future
	// change in the upstream btcutil defaults cannot silently move them.
	MainNetParams = chaincfg.MainNetParams
	MainNetParams.Net = MainnetMagic
	MainNetParams.DefaultPort = "8333"                             // nDefaultPort, src/chainparams.cpp:157
	MainNetParams.PubKeyHashAddrID = []byte{0}                     // base58Prefixes[PUBKEY_ADDRESS], src/chainparams.cpp:177
	MainNetParams.ScriptHashAddrID = []byte{5}                     // base58Prefixes[SCRIPT_ADDRESS], src/chainparams.cpp:178
	MainNetParams.PrivateKeyID = []byte{128}                       // base58Prefixes[SECRET_KEY], src/chainparams.cpp:179
	MainNetParams.HDPublicKeyID = [4]byte{0x04, 0x88, 0xB2, 0x1E}  // base58Prefixes[EXT_PUBLIC_KEY], src/chainparams.cpp:180
	MainNetParams.HDPrivateKeyID = [4]byte{0x04, 0x88, 0xAD, 0xE4} // base58Prefixes[EXT_SECRET_KEY], src/chainparams.cpp:181
	// Bitcoin SV has no segwit and therefore no bech32 human readable part -
	// the string "bech32" does not occur anywhere in the node sources. Clearing
	// it keeps btcutil.DecodeAddress from ever accepting a bech32 address for
	// this chain (chaincfg.Register would otherwise publish "bc1" globally).
	MainNetParams.Bech32HRPSegwit = ""
	// SLIP-0044 coin type of Bitcoin SV. Not part of the node sources; the node
	// has no wallet-level BIP44 coin type. Unused by blockbook, kept for
	// completeness next to the Slip44 configuration option.
	MainNetParams.HDCoinType = 236

	TestNetParams = chaincfg.TestNet3Params
	TestNetParams.Net = TestnetMagic
	TestNetParams.DefaultPort = "18333"                            // nDefaultPort, src/chainparams.cpp:429
	TestNetParams.PubKeyHashAddrID = []byte{111}                   // src/chainparams.cpp:454
	TestNetParams.ScriptHashAddrID = []byte{196}                   // src/chainparams.cpp:455
	TestNetParams.PrivateKeyID = []byte{239}                       // src/chainparams.cpp:456
	TestNetParams.HDPublicKeyID = [4]byte{0x04, 0x35, 0x87, 0xCF}  // src/chainparams.cpp:457
	TestNetParams.HDPrivateKeyID = [4]byte{0x04, 0x35, 0x83, 0x94} // src/chainparams.cpp:458
	TestNetParams.Bech32HRPSegwit = ""

	// STN, the scaling test network, shares the testnet base58 prefixes and
	// differs only in the network magic and the default port.
	StnParams = chaincfg.TestNet3Params
	StnParams.Name = "stn"
	StnParams.Net = StnMagic
	StnParams.DefaultPort = "9333"                             // nDefaultPort, src/chainparams.cpp:318
	StnParams.PubKeyHashAddrID = []byte{111}                   // src/chainparams.cpp:328
	StnParams.ScriptHashAddrID = []byte{196}                   // src/chainparams.cpp:329
	StnParams.PrivateKeyID = []byte{239}                       // src/chainparams.cpp:330
	StnParams.HDPublicKeyID = [4]byte{0x04, 0x35, 0x87, 0xCF}  // src/chainparams.cpp:331
	StnParams.HDPrivateKeyID = [4]byte{0x04, 0x35, 0x83, 0x94} // src/chainparams.cpp:332
	StnParams.Bech32HRPSegwit = ""

	RegtestParams = chaincfg.RegressionNetParams
	RegtestParams.Net = RegtestMagic
	RegtestParams.DefaultPort = "18444"                            // nDefaultPort, src/chainparams.cpp:556
	RegtestParams.PubKeyHashAddrID = []byte{111}                   // src/chainparams.cpp:585
	RegtestParams.ScriptHashAddrID = []byte{196}                   // src/chainparams.cpp:586
	RegtestParams.PrivateKeyID = []byte{239}                       // src/chainparams.cpp:587
	RegtestParams.HDPublicKeyID = [4]byte{0x04, 0x35, 0x87, 0xCF}  // src/chainparams.cpp:588
	RegtestParams.HDPrivateKeyID = [4]byte{0x04, 0x35, 0x83, 0x94} // src/chainparams.cpp:589
	RegtestParams.Bech32HRPSegwit = ""
}

// BSVParser handle
type BSVParser struct {
	*btc.BitcoinLikeParser
}

// NewBSVParser returns new BSVParser instance.
//
// Bitcoin SV knows exactly one address encoding, the base58 one it inherited
// from Bitcoin, so the only accepted address_format values are the empty string
// and "legacy". Anything else - notably the "cashaddr" that a config copied
// from bcash would carry - is rejected instead of silently ignored: an index
// built with the wrong address encoding is unusable and has to be resynced.
func NewBSVParser(params *chaincfg.Params, c *btc.Configuration) (*BSVParser, error) {
	switch c.AddressFormat {
	case "", "legacy":
	default:
		return nil, fmt.Errorf("unsupported address format for Bitcoin SV: %s", c.AddressFormat)
	}
	p := &BSVParser{
		BitcoinLikeParser: btc.NewBitcoinLikeParser(params, c),
	}
	// Bitcoin SV has no segwit, so there is no virtual size distinct from the
	// serialized size and getrawtransaction does not return a "vsize" field
	// (the string does not appear in src/rpc/rawtransaction.cpp). Leaving
	// VSizeSupport false keeps blockbook from reporting an empty vsize.
	p.VSizeSupport = false
	return p, nil
}

// GetChainParams contains network parameters for the main Bitcoin SV network,
// the test Bitcoin SV network, the scaling test network and the regression test
// Bitcoin SV network. The chain names are the ones returned in the "chain"
// field of getblockchaininfo, see CBaseChainParams in
// src/chainparamsbase.cpp:12-16.
func GetChainParams(chain string) *chaincfg.Params {
	if !chaincfg.IsRegistered(&MainNetParams) {
		err := chaincfg.Register(&MainNetParams)
		if err == nil {
			err = chaincfg.Register(&TestNetParams)
		}
		if err == nil {
			err = chaincfg.Register(&StnParams)
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
	case "stn":
		return &StnParams
	case "regtest":
		return &RegtestParams
	default:
		return &MainNetParams
	}
}
