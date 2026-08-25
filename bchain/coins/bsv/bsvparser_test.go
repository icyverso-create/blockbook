//go:build unittest

package bsv

import (
	"encoding/hex"
	"os"
	"reflect"
	"testing"

	"github.com/martinboehm/btcutil/chaincfg"
	"github.com/trezor/blockbook/bchain/coins/btc"
)

func TestMain(m *testing.M) {
	c := m.Run()
	chaincfg.ResetParams()
	os.Exit(c)
}

func newTestParser(t *testing.T, chain string, c *btc.Configuration) *BSVParser {
	t.Helper()
	p, err := NewBSVParser(GetChainParams(chain), c)
	if err != nil {
		t.Fatalf("NewBSVParser() error = %v", err)
	}
	return p
}

// Bitcoin SV inherited Bitcoin's base58 prefixes unchanged
// (base58Prefixes[PUBKEY_ADDRESS] = 0, [SCRIPT_ADDRESS] = 5,
// bitcoin-sv-1.2.1/src/chainparams.cpp:177-178), so the well known Bitcoin
// mainnet addresses below must map to the very same output scripts.
func Test_GetAddrDescFromAddress_Mainnet(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
		wantErr bool
	}{
		{
			name:    "P2PKH genesis coinbase address",
			address: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
			want:    "76a91462e907b15cbf27d5425399ebf6f0fb50ebb88f1888ac",
			wantErr: false,
		},
		{
			name:    "P2SH",
			address: "3P14159f73E4gFr7JterCCQh9QjiTjiZrG",
			want:    "a914e9c3dd0c07aac76179ebc76a6c78d4d67c6c160a87",
			wantErr: false,
		},
		{
			name:    "bech32 is not a Bitcoin SV address",
			address: "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq",
			want:    "",
			wantErr: true,
		},
		{
			name:    "cashaddr is not a Bitcoin SV address",
			address: "bitcoincash:qpm2qsznhks23z7629mms6s4cwef74vcwvy22gdx6a",
			want:    "",
			wantErr: true,
		},
	}
	parser := newTestParser(t, "main", &btc.Configuration{})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.GetAddrDescFromAddress(tt.address)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAddrDescFromAddress() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			h := hex.EncodeToString(got)
			if !reflect.DeepEqual(h, tt.want) {
				t.Errorf("GetAddrDescFromAddress() = %v, want %v", h, tt.want)
			}
		})
	}
}

func Test_GetAddressesFromAddrDesc_Mainnet(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		want       []string
		wantSearch bool
	}{
		{
			name:       "P2PKH",
			script:     "76a91462e907b15cbf27d5425399ebf6f0fb50ebb88f1888ac",
			want:       []string{"1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"},
			wantSearch: true,
		},
		{
			name:       "P2SH",
			script:     "a914e9c3dd0c07aac76179ebc76a6c78d4d67c6c160a87",
			want:       []string{"3P14159f73E4gFr7JterCCQh9QjiTjiZrG"},
			wantSearch: true,
		},
		{
			// Bitcoin SV blocks are dominated by data carrier outputs; they
			// must not be turned into searchable addresses.
			name:       "OP_RETURN",
			script:     "6a0568656c6c6f",
			want:       []string{"OP_RETURN (hello)"},
			wantSearch: false,
		},
	}
	parser := newTestParser(t, "main", &btc.Configuration{})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := hex.DecodeString(tt.script)
			if err != nil {
				t.Fatal(err)
			}
			got, gotSearch, err := parser.GetAddressesFromAddrDesc(b)
			if err != nil {
				t.Fatalf("GetAddressesFromAddrDesc() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetAddressesFromAddrDesc() = %v, want %v", got, tt.want)
			}
			if gotSearch != tt.wantSearch {
				t.Errorf("GetAddressesFromAddrDesc() searchable = %v, want %v", gotSearch, tt.wantSearch)
			}
		})
	}
}

// The address format is not configurable on Bitcoin SV; a leftover "cashaddr"
// from a bcash-derived config must fail loudly rather than be ignored.
func Test_NewBSVParser_AddressFormat(t *testing.T) {
	tests := []struct {
		format  string
		wantErr bool
	}{
		{format: "", wantErr: false},
		{format: "legacy", wantErr: false},
		{format: "cashaddr", wantErr: true},
		{format: "whatever", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			_, err := NewBSVParser(GetChainParams("main"), &btc.Configuration{AddressFormat: tt.format})
			if (err != nil) != tt.wantErr {
				t.Errorf("NewBSVParser(%q) error = %v, wantErr %v", tt.format, err, tt.wantErr)
			}
		})
	}
}

func Test_GetChainParams(t *testing.T) {
	tests := []struct {
		chain string
		want  *chaincfg.Params
	}{
		{chain: "main", want: &MainNetParams},
		{chain: "", want: &MainNetParams},
		{chain: "test", want: &TestNetParams},
		{chain: "stn", want: &StnParams},
		{chain: "regtest", want: &RegtestParams},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			if got := GetChainParams(tt.chain); got != tt.want {
				t.Errorf("GetChainParams(%q) = %v, want %v", tt.chain, got.Name, tt.want.Name)
			}
		})
	}
	// the four networks must be distinguishable, otherwise chaincfg.Register
	// would silently drop one of them
	nets := map[uint32]string{}
	for _, p := range []*chaincfg.Params{&MainNetParams, &TestNetParams, &StnParams, &RegtestParams} {
		if other, ok := nets[uint32(p.Net)]; ok {
			t.Errorf("network magic collision between %s and %s", other, p.Name)
		}
		nets[uint32(p.Net)] = p.Name
	}
}
