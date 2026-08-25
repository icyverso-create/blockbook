//go:build unittest

package common

import (
	"bytes"
	"encoding/hex"
	"io"
	"math/rand"
	"strings"
	"testing"
)

// shortReader returns at most n bytes per Read, to exercise the refill logic and
// value boundaries that land in the middle of the read buffer.
type shortReader struct {
	data []byte
	n    int
}

func (r *shortReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.n
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestDecodeJSONRPCHexResult_ok(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"result first", `{"result":"00ff10ab","error":null,"id":"blockbook"}`, "00ff10ab"},
		{"error first", `{"error":null,"result":"deadbeef","id":1}`, "deadbeef"},
		{"uppercase hex", `{"result":"DEADBEEF","error":null,"id":1}`, "deadbeef"},
		{"empty result", `{"result":"","error":null,"id":1}`, ""},
		{"whitespace", "{\n\t\"result\" : \"0a0b\" ,\n\t\"error\" : null\n}", "0a0b"},
		{"no error member", `{"result":"0a0b","id":1}`, "0a0b"},
		{"trailing members", `{"result":"0a0b","error":null,"id":1,"extra":{"a":[1,2,"x"]}}`, "0a0b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, chunk := range []int{1, 3, 1024} {
				res, err := DecodeJSONRPCHexResult(&shortReader{data: []byte(tt.body), n: chunk}, 0)
				if err != nil {
					t.Fatalf("chunk %d: unexpected error %v", chunk, err)
				}
				if !res.ResultIsString {
					t.Fatalf("chunk %d: ResultIsString = false", chunk)
				}
				if res.Error != nil {
					t.Fatalf("chunk %d: unexpected rpc error %s", chunk, res.Error)
				}
				if got := hex.EncodeToString(res.Result); got != tt.want {
					t.Errorf("chunk %d: got %q, want %q", chunk, got, tt.want)
				}
			}
		})
	}
}

func TestDecodeJSONRPCHexResult_rpcError(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"result null after error", `{"error":{"code":-5,"message":"Block not found"},"result":null,"id":1}`},
		{"result null before error", `{"result":null,"error":{"code":-5,"message":"Block not found"},"id":1}`},
		{"error only", `{"error":{"code":-5,"message":"Block not found"}}`},
		// pathological: a backend that sends both. The error must win and the
		// decoded result must be dropped.
		{"result string and error", `{"result":"0a0b","error":{"code":-5,"message":"Block not found"},"id":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := DecodeJSONRPCHexResult(strings.NewReader(tt.body), 0)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if res.Error == nil {
				t.Fatal("expected an rpc error")
			}
			if !strings.Contains(string(res.Error), "Block not found") {
				t.Errorf("error not captured verbatim: %s", res.Error)
			}
			if res.Result != nil || res.ResultIsString {
				t.Errorf("result must be discarded when an error is present, got %v", res.Result)
			}
		})
	}
}

func TestDecodeJSONRPCHexResult_resultNullNoError(t *testing.T) {
	res, err := DecodeJSONRPCHexResult(strings.NewReader(`{"result":null,"error":null,"id":1}`), 0)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if res.ResultIsString || res.Result != nil || res.Error != nil {
		t.Errorf("unexpected %+v", res)
	}
}

func TestDecodeJSONRPCHexResult_malformed(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"odd hex", `{"result":"0a0","error":null}`, "odd number of hex characters"},
		{"garbage in hex", `{"result":"0a0zff","error":null}`, "invalid hex result"},
		{"json escape in hex", "{\"result\":\"0a\\u0062\",\"error\":null}", "invalid hex result"},
		{"binary garbage in hex", "{\"result\":\"0a\x00\x01\",\"error\":null}", "invalid hex result"},
		{"truncated in hex", `{"result":"0a0b0c`, "unexpected EOF"},
		{"truncated before result", `{"resu`, "unexpected EOF"},
		{"truncated after result", `{"result":"0a0b","err`, "unexpected EOF"},
		{"empty body", ``, "unexpected EOF"},
		{"not an object", `[1,2,3]`, "expected object"},
		{"html error page", `<html><body>502</body></html>`, "expected object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// chunk sizes 1..3 make the bad byte land at either half of a hex
			// pair and on both sides of a read-buffer boundary
			for _, chunk := range []int{1, 2, 3, 1024} {
				_, err := DecodeJSONRPCHexResult(&shortReader{data: []byte(tt.body), n: chunk}, 0)
				if err == nil {
					t.Fatalf("chunk %d: expected an error", chunk)
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("chunk %d: got error %q, want it to contain %q", chunk, err, tt.want)
				}
			}
		})
	}
}

// TestDecodeJSONRPCHexResult_badByteAtEveryOffset walks a bad character through
// every position of a hex string that spans several read buffers, so the
// pair-wise inner loop, the carry-over nibble and the chunk seam are all hit.
func TestDecodeJSONRPCHexResult_badByteAtEveryOffset(t *testing.T) {
	good := strings.Repeat("ab", 8)
	for pos := 0; pos < len(good); pos++ {
		bad := []byte(good)
		bad[pos] = 'z'
		body := `{"result":"` + string(bad) + `","error":null}`
		for _, chunk := range []int{1, 2, 3, 5, 1024} {
			_, err := DecodeJSONRPCHexResult(&shortReader{data: []byte(body), n: chunk}, 0)
			if err == nil {
				t.Fatalf("pos %d chunk %d: expected an error", pos, chunk)
			}
			if !strings.Contains(err.Error(), "invalid hex result") {
				t.Fatalf("pos %d chunk %d: got %q", pos, chunk, err)
			}
		}
	}
}

// TestDecodeJSONRPCHexResult_oddLengthAtEveryChunking checks the carry-over
// nibble logic for hex strings of every length up to a few chunks.
func TestDecodeJSONRPCHexResult_oddLengthAtEveryChunking(t *testing.T) {
	for n := 0; n <= 24; n++ {
		want := strings.Repeat("5c", n/2)
		if n%2 == 1 {
			want += "5"
		}
		body := `{"result":"` + want + `","error":null}`
		for _, chunk := range []int{1, 2, 3, 7, 1024} {
			res, err := DecodeJSONRPCHexResult(&shortReader{data: []byte(body), n: chunk}, 0)
			if n%2 == 1 {
				if err == nil || !strings.Contains(err.Error(), "odd number of hex characters") {
					t.Fatalf("n %d chunk %d: want odd-length error, got %v", n, chunk, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("n %d chunk %d: %v", n, chunk, err)
			}
			if got := hex.EncodeToString(res.Result); got != want {
				t.Fatalf("n %d chunk %d: got %q want %q", n, chunk, got, want)
			}
		}
	}
}

// TestDecodeJSONRPCHexResult_big feeds a payload several times larger than the
// internal read buffer so that hex pairs, the closing quote and the trailing
// members all straddle chunk boundaries.
func TestDecodeJSONRPCHexResult_big(t *testing.T) {
	block := make([]byte, 3*hexStreamBufSize+7)
	rnd := rand.New(rand.NewSource(1))
	rnd.Read(block)
	var body bytes.Buffer
	body.WriteString(`{"result":"`)
	body.WriteString(hex.EncodeToString(block))
	body.WriteString(`","error":null,"id":"blockbook"}`)

	for _, hint := range []int{0, len(block), len(block) - 1000, len(block) + 1000, HexStreamMaxSizeHint + 1, -5} {
		res, err := DecodeJSONRPCHexResult(bytes.NewReader(body.Bytes()), hint)
		if err != nil {
			t.Fatalf("hint %d: unexpected error %v", hint, err)
		}
		if !bytes.Equal(res.Result, block) {
			t.Fatalf("hint %d: decoded block differs", hint)
		}
	}
}

// TestDecodeJSONRPCHexResult_sizeHintAllocatesOnce documents the point of the
// exercise: with a correct hint the destination buffer is never regrown.
func TestDecodeJSONRPCHexResult_sizeHintAllocatesOnce(t *testing.T) {
	block := make([]byte, 1<<20)
	var body bytes.Buffer
	body.WriteString(`{"result":"`)
	body.WriteString(hex.EncodeToString(block))
	body.WriteString(`","error":null}`)
	res, err := DecodeJSONRPCHexResult(bytes.NewReader(body.Bytes()), len(block))
	if err != nil {
		t.Fatal(err)
	}
	if c := cap(res.Result); c != len(block)+8 {
		t.Errorf("buffer was reallocated: cap %d, want %d", c, len(block)+8)
	}
}

func TestDecodeJSONRPCHexResult_hugeErrorMemberIsBounded(t *testing.T) {
	var body bytes.Buffer
	body.WriteString(`{"result":null,"error":{"code":-5,"message":"`)
	body.WriteString(strings.Repeat("x", 4*hexStreamAuxLimit))
	body.WriteString(`"},"id":1}`)
	res, err := DecodeJSONRPCHexResult(bytes.NewReader(body.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Error) > hexStreamAuxLimit {
		t.Errorf("error member not bounded: %d bytes", len(res.Error))
	}
}
