package bsv

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/trezor/blockbook/bchain/coins/btc"
)

// Сравнивает НАШ ParseBlock со СТОКОВЫМ на настоящем блоке BSV.
// Проверяется главное: дескрипторы адресов обязаны совпасть до байта.
func TestParseBlockEquivalence(t *testing.T) {
	// A real block is far more convincing than a synthetic one, but it is too
	// big to commit. Point BSV_BLOCK_JSON at the raw response of
	// `getblock <hash> 0` to run this; the test skips without it.
	path := os.Getenv("BSV_BLOCK_JSON")
	if path == "" {
		t.Skip("BSV_BLOCK_JSON not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skip("cannot read block:", err)
	}
	var res struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	blockBytes, err := hex.DecodeString(res.Result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("блок: %d байт", len(blockBytes))

	cfg := &btc.Configuration{AddressFormat: "legacy"}
	ours, err := NewBSVParser(GetChainParams("main"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	stock := btc.NewBitcoinLikeParser(GetChainParams("main"), cfg)

	ourBlock, err := ours.ParseBlock(blockBytes)
	if err != nil {
		t.Fatalf("наш ParseBlock: %v", err)
	}
	stockBlock, err := stock.ParseBlock(blockBytes)
	if err != nil {
		t.Fatalf("стоковый ParseBlock: %v", err)
	}

	if len(ourBlock.Txs) != len(stockBlock.Txs) {
		t.Fatalf("разное число транзакций: наш %d, сток %d", len(ourBlock.Txs), len(stockBlock.Txs))
	}
	t.Logf("транзакций: %d", len(ourBlock.Txs))

	var outs, ins, mismatch, addrNonEmpty int
	for ti := range ourBlock.Txs {
		a, b := &ourBlock.Txs[ti], &stockBlock.Txs[ti]
		if a.Txid != b.Txid {
			t.Fatalf("tx %d: txid разошёлся: %s vs %s", ti, a.Txid, b.Txid)
		}
		if a.Version != b.Version || a.LockTime != b.LockTime {
			t.Fatalf("tx %d: version/locktime разошлись", ti)
		}
		if len(a.Vin) != len(b.Vin) || len(a.Vout) != len(b.Vout) {
			t.Fatalf("tx %d: разное число входов/выходов", ti)
		}
		for i := range a.Vin {
			ins++
			if a.Vin[i].Txid != b.Vin[i].Txid || a.Vin[i].Vout != b.Vin[i].Vout {
				t.Fatalf("tx %d вход %d: outpoint разошёлся", ti, i)
			}
			if a.Vin[i].Coinbase != b.Vin[i].Coinbase {
				t.Fatalf("tx %d вход %d: coinbase разошёлся", ti, i)
			}
		}
		for i := range a.Vout {
			outs++
			if a.Vout[i].ValueSat.Cmp(&b.Vout[i].ValueSat) != 0 {
				t.Fatalf("tx %d выход %d: СУММА разошлась: %s vs %s", ti, i,
					a.Vout[i].ValueSat.String(), b.Vout[i].ValueSat.String())
			}
			// ГЛАВНОЕ: дескриптор адреса
			ad1, e1 := ours.GetAddrDescFromVout(&a.Vout[i])
			ad2, e2 := stock.GetAddrDescFromVout(&b.Vout[i])
			if (e1 == nil) != (e2 == nil) {
				t.Fatalf("tx %d выход %d: разное наличие ошибки: %v vs %v", ti, i, e1, e2)
			}
			if !bytes.Equal(ad1, ad2) {
				mismatch++
				if mismatch < 4 {
					t.Errorf("tx %d выход %d: АДРЕС РАЗОШЁЛСЯ: %x vs %x", ti, i, ad1, ad2)
				}
			}
			if len(ad1) > 0 {
				addrNonEmpty++
			}
		}
	}
	t.Logf("входов: %d, выходов: %d, непустых дескрипторов: %d", ins, outs, addrNonEmpty)
	if mismatch != 0 {
		t.Fatalf("РАСХОЖДЕНИЙ ПО АДРЕСАМ: %d", mismatch)
	}
	t.Logf("все %d дескрипторов адресов совпали", outs)
}
