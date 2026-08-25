package btc

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/trezor/blockbook/bchain"
)

func testRPC(url string) *BitcoinRPC {
	ctx, cancel := context.WithCancel(context.Background())
	return &BitcoinRPC{
		BaseChain:    &bchain.BaseChain{},
		client:       http.Client{Timeout: 10 * time.Second},
		rpcURL:       url,
		RPCMarshaler: JSONMarshalerV2{},
		callCtx:      ctx,
		cancelCall:   cancel,
	}
}

// TestGetBlockBytesStreamed checks that the streamed getblock path returns the
// same bytes as the stock hex.DecodeString(GetBlockRaw(...)) path did, for a
// payload big enough to cross the internal read buffer several times.
func TestGetBlockBytesStreamed(t *testing.T) {
	block := bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x7f}, 300000) // 1.8 MB
	body := []byte(`{"result":"` + hex.EncodeToString(block) + `","error":null,"id":"blockbook"}`)

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()
	b := testRPC(srv.URL)

	for _, hint := range []int{0, len(block)} {
		data, err := b.GetBlockBytesWithSizeHint("000000000000000001", hint)
		if err != nil {
			t.Fatalf("hint %d: %v", hint, err)
		}
		if !bytes.Equal(data, block) {
			t.Fatalf("hint %d: decoded block differs (got %d bytes, want %d)", hint, len(data), len(block))
		}
	}
	// the request itself must be byte-identical to what Call would have sent
	if !bytes.Contains(gotBody, []byte(`"method":"getblock"`)) || !bytes.Contains(gotBody, []byte(`"verbosity":0`)) {
		t.Errorf("unexpected request body: %s", gotBody)
	}
}

// TestGetBlockBytesContentLengthHint verifies the buffer is sized from
// Content-Length when the caller has no size hint (the sync path via
// GetBlockWithoutHeader), so a 3.8 GiB BSV block costs one allocation.
func TestGetBlockBytesContentLengthHint(t *testing.T) {
	block := bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 250000) // 1 MB
	body := []byte(`{"result":"` + hex.EncodeToString(block) + `","error":null,"id":1}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// net/http sets Content-Length automatically for a single small-enough
		// write; set it explicitly so the test does not depend on that.
		w.Header().Set("Content-Length", itoa(len(body)))
		w.Write(body)
	}))
	defer srv.Close()

	data, err := testRPC(srv.URL).GetBlockBytes("000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, block) {
		t.Fatal("decoded block differs")
	}
	// cap == len(body)/2 + 8 proves the hint came from Content-Length and that
	// append never had to grow the buffer
	if want := len(body)/2 + 8; cap(data) != want {
		t.Errorf("buffer regrown: cap %d, want %d", cap(data), want)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// TestGetBlockBytesErrors covers every way the backend can say "no block", plus
// a mid-body disconnect.
func TestGetBlockBytesErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		hijack  bool
		wantErr error
		wantMsg string
	}{
		{
			// bitcoind replies 500 with a well-formed JSON-RPC error for -5
			name:    "not found with http 500",
			status:  500,
			body:    `{"result":null,"error":{"code":-5,"message":"Block not found"},"id":1}`,
			wantErr: bchain.ErrBlockNotFound,
		},
		{
			name:    "not found with http 200",
			status:  200,
			body:    `{"result":null,"error":{"code":-5,"message":"Block not found"},"id":1}`,
			wantErr: bchain.ErrBlockNotFound,
		},
		{
			name:    "height out of range",
			status:  200,
			body:    `{"result":null,"error":{"code":-8,"message":"Block height out of range"},"id":1}`,
			wantErr: bchain.ErrBlockNotFound,
		},
		{
			name:    "other rpc error",
			status:  200,
			body:    `{"result":null,"error":{"code":-32601,"message":"Method not found"},"id":1}`,
			wantMsg: "Method not found",
		},
		{
			name:    "result missing",
			status:  200,
			body:    `{"id":1}`,
			wantMsg: "missing result",
		},
		{
			name:    "odd hex",
			status:  200,
			body:    `{"result":"0a0b0","error":null,"id":1}`,
			wantMsg: "odd number of hex characters",
		},
		{
			name:    "garbage instead of hex",
			status:  200,
			body:    `{"result":"not hex at all","error":null,"id":1}`,
			wantMsg: "invalid hex result",
		},
		{
			name:    "html error page",
			status:  200,
			body:    `<html>502 Bad Gateway</html>`,
			wantMsg: "expected object",
		},
		{
			name:    "connection dropped mid block",
			status:  200,
			body:    `{"result":"0a0b0c0d0e0f`,
			hijack:  true,
			wantMsg: "unexpected EOF",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.hijack {
					// announce a 1 MB body, send a few bytes of it, then close
					// the socket cleanly - exactly what a backend restart or a
					// proxy timeout looks like to the client
					hj, ok := w.(http.Hijacker)
					if !ok {
						return
					}
					c, bw, err := hj.Hijack()
					if err != nil {
						return
					}
					defer c.Close()
					bw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 1000000\r\n\r\n")
					bw.WriteString(tt.body)
					bw.Flush()
					return
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := testRPC(srv.URL).GetBlockBytes("000000000000000001")
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("got %v (%T), want the sentinel %v", err, err, tt.wantErr)
				}
				return
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tt.wantMsg)) {
				t.Errorf("got error %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

// TestGetBlockBytesReusesConnection checks the streaming path does not leak
// connections: 10 sequential getblock calls must share one TCP connection.
//
// The server answers with Transfer-Encoding: chunked and no Content-Length,
// which is what bitcoind actually does for getblock (verified against a live
// Bitcoin SV node). Note this passes with and without the explicit drain in
// callStreamedHex - the leftover after a decoded block is a few bytes and
// net/http consumes those on its own. It is a guard against a future change
// that abandons the body with real data still pending, not proof that the
// drain is what keeps the connection alive.
func TestGetBlockBytesReusesConnection(t *testing.T) {
	block := bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 64*1024) // 256 kB, several read buffers
	body := []byte(`{"result":"` + hex.EncodeToString(block) + `","error":null,"id":"blockbook"}` + "\n")

	var mu sync.Mutex
	conns := make(map[string]struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// flushing before the payload commits the response to chunked encoding,
		// exactly as bitcoind does
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		w.Write(body)
		w.(http.Flusher).Flush()
	}))
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		if s == http.StateNew {
			mu.Lock()
			conns[c.RemoteAddr().String()] = struct{}{}
			mu.Unlock()
		}
	}
	srv.Start()
	defer srv.Close()
	b := testRPC(srv.URL)

	const requests = 10
	for i := 0; i < requests; i++ {
		data, err := b.GetBlockBytesWithSizeHint("000000000000000001", 0)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if len(data) != len(block) {
			t.Fatalf("request %d: got %d bytes, want %d", i, len(data), len(block))
		}
	}
	mu.Lock()
	n := len(conns)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("%d requests opened %d connections, want 1 - the response body is not drained before Close", requests, n)
	}
}
