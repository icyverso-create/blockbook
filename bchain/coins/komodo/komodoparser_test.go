//go:build unittest

package komodo

import (
	"encoding/hex"
	"testing"

	"github.com/martinboehm/btcutil/chaincfg"
	"github.com/trezor/blockbook/bchain/coins/btc"
)

func init() {
	c := chaincfg.MainNetParams
	c.Net = MainnetMagic
	if !chaincfg.IsRegistered(&c) {
		_ = chaincfg.Register(&MainNetParams)
	}
}

func newTestParser() *KomodoParser {
	return NewKomodoParser(GetChainParams("main"), &btc.Configuration{BlockAddressesToKeep: 1})
}

// TestAddressToOutputScript checks that Komodo's single-byte base58 prefixes
// (60 for P2PKH, 85 for P2SH) produce the addresses komodod itself reports.
func TestAddressToOutputScript(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{
			// mainnet output of tx 4fb1ccfb37a48384efceea1796b8778d0e02164d5c8ed78869f55fa803cffd1e
			name:    "P2PKH",
			address: "RVtg94k4yjx5JznUt47fpHZkWC2wA5KqCQ",
			want:    "76a914e2187fa71d3c38b349de7218a9d13dfaa7fcdd4988ac",
		},
	}
	parser := newTestParser()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.GetAddrDescFromAddress(tt.address)
			if err != nil {
				t.Fatalf("GetAddrDescFromAddress() error = %v", err)
			}
			if h := hex.EncodeToString(got); h != tt.want {
				t.Errorf("GetAddrDescFromAddress() = %v, want %v", h, tt.want)
			}
		})
	}
}

// TestOutputScriptToAddress is the reverse direction of the same mapping.
func TestOutputScriptToAddress(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "P2PKH",
			script: "76a914e2187fa71d3c38b349de7218a9d13dfaa7fcdd4988ac",
			want:   []string{"RVtg94k4yjx5JznUt47fpHZkWC2wA5KqCQ"},
		},
	}
	parser := newTestParser()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := hex.DecodeString(tt.script)
			if err != nil {
				t.Fatalf("DecodeString() error = %v", err)
			}
			got, _, err := parser.GetAddressesFromAddrDesc(b)
			if err != nil {
				t.Fatalf("GetAddressesFromAddrDesc() error = %v", err)
			}
			if len(got) != len(tt.want) || (len(got) > 0 && got[0] != tt.want[0]) {
				t.Errorf("GetAddressesFromAddrDesc() = %v, want %v", got, tt.want)
			}
		})
	}
}
