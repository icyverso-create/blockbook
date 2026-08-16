//go:build unittest

package verge

import (
	"testing"

	"github.com/trezor/blockbook/bchain/coins/btc"
	"github.com/trezor/blockbook/common"
)

func TestVergeAmounts(t *testing.T) {
	p := NewVergeParser(GetChainParams("main"), &btc.Configuration{})
	if p.AmountDecimals() != 6 {
		t.Fatalf("AmountDecimals() = %d, want 6", p.AmountDecimals())
	}
	for _, tt := range []struct{ in, want string }{
		{"50.123457", "50123457"},
		{"1321.578906", "1321578906"},
		{"1551.22046", "1551220460"},
		{"0.000001", "1"},
		{"1", "1000000"},
		{"0", "0"},
	} {
		got, err := p.AmountToBigInt(common.JSONNumber(tt.in))
		if err != nil {
			t.Fatalf("AmountToBigInt(%s): %v", tt.in, err)
		}
		if got.String() != tt.want {
			t.Errorf("AmountToBigInt(%s) = %s, want %s", tt.in, got.String(), tt.want)
		}
		back := p.AmountToDecimalString(&got)
		t.Logf("  %-14s -> %-12s -> %s", tt.in, got.String(), back)
	}
}
