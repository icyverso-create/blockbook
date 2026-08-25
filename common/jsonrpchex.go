package common

import (
	"bytes"
	"encoding/json"
	"io"
	"runtime/debug"

	"github.com/golang/glog"
	"github.com/juju/errors"
)

// Streaming decoder for JSON-RPC responses whose "result" member is one huge
// hex string (getblock verbosity=0 on big-block chains such as Bitcoin SV).
//
// The stock path (io.ReadAll + json.Unmarshal + hex.DecodeString) materializes
// three copies of the payload at once: the raw JSON body (2*N bytes), the
// unmarshalled Go string (2*N bytes) and the decoded block (N bytes), i.e. 5*N.
// For a 3814 MiB BSV block that is ~19 GiB before the parser even starts.
//
// DecodeJSONRPCHexResult walks the response body with a small fixed read buffer
// and converts hex to bytes on the fly, so the peak is one N-byte buffer.

const (
	// hexStreamBufSize is the fixed read buffer used while streaming the body.
	hexStreamBufSize = 512 * 1024
	// hexStreamAuxLimit caps how many bytes of any member other than a hex
	// "result" are retained (the "error" object, "id", ...). Anything past the
	// limit is consumed and dropped, so a hostile/broken backend cannot make us
	// buffer an unbounded non-result member.
	hexStreamAuxLimit = 1 << 20
	// hexStreamKeyLimit caps the length of a retained member name.
	hexStreamKeyLimit = 64
	// hexStreamDefaultCap is the initial capacity of the decode buffer when no
	// size hint is available; from there it doubles (see reserve).
	hexStreamDefaultCap = 64 * 1024
	// HexStreamMaxSizeHint is the largest size hint honored for pre-allocation.
	// A bigger hint is treated as unknown so a bogus Content-Length cannot make
	// us request an absurd allocation.
	HexStreamMaxSizeHint = 8 << 30
)

// hexNibbles maps an ASCII byte to its hex value, 0xff meaning "not a hex digit".
var hexNibbles = func() (t [256]byte) {
	for i := range t {
		t[i] = 0xff
	}
	for i := byte('0'); i <= '9'; i++ {
		t[i] = i - '0'
	}
	for i := byte('a'); i <= 'f'; i++ {
		t[i] = i - 'a' + 10
	}
	for i := byte('A'); i <= 'F'; i++ {
		t[i] = i - 'A' + 10
	}
	return
}()

// JSONRPCHexResult is the outcome of DecodeJSONRPCHexResult.
type JSONRPCHexResult struct {
	// Result holds the bytes decoded from the hex string in the "result"
	// member. It is nil when "result" was absent or was not a JSON string
	// (typically null, which is what a backend sends together with an error).
	Result []byte
	// ResultIsString is true when "result" was a JSON string, including an
	// empty one - it distinguishes "" from null.
	ResultIsString bool
	// Error holds the raw JSON of the "error" member, nil when the member was
	// absent or null. It is left raw so that this package does not have to
	// import bchain; the caller unmarshals it into its own error type.
	Error json.RawMessage
}

// DecodeJSONRPCHexResult reads a JSON-RPC response from r and decodes the hex
// string in its "result" member straight into a byte slice, without ever
// holding the JSON body or the hex string in memory.
//
// sizeHint, when positive, is the expected size in bytes of the decoded result
// and is used to size the destination buffer with a single allocation; pass 0
// when unknown and the buffer grows exponentially instead.
//
// If the "error" member is seen before "result" and is not null, the "result"
// member is skipped instead of being decoded.
func DecodeJSONRPCHexResult(r io.Reader, sizeHint int) (res JSONRPCHexResult, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			glog.Error("DecodeJSONRPCHexResult recovered from panic: ", rec)
			debug.PrintStack()
			res = JSONRPCHexResult{}
			err = errors.New("Internal error")
		}
	}()
	if sizeHint < 0 || sizeHint > HexStreamMaxSizeHint {
		sizeHint = 0
	}
	s := &jsonStream{r: r, buf: make([]byte, hexStreamBufSize)}
	c, err := s.nextNonSpace()
	if err != nil {
		return res, err
	}
	if c != '{' {
		return res, errors.Errorf("invalid JSON-RPC response, expected object, got %q", string(c))
	}
	for {
		c, err = s.nextNonSpace()
		if err != nil {
			return res, err
		}
		if c == '}' {
			return res, nil
		}
		if c == ',' {
			continue
		}
		if c != '"' {
			return res, errors.Errorf("invalid JSON-RPC response, expected member name, got %q", string(c))
		}
		key, err := s.readStringBody(hexStreamKeyLimit)
		if err != nil {
			return res, err
		}
		if c, err = s.nextNonSpace(); err != nil {
			return res, err
		}
		if c != ':' {
			return res, errors.Errorf("invalid JSON-RPC response, expected ':' after %q, got %q", string(key), string(c))
		}
		if c, err = s.nextNonSpace(); err != nil {
			return res, err
		}
		switch string(key) {
		case "result":
			// an error already seen wins - do not spend memory decoding
			if c == '"' && res.Error == nil {
				data, derr := s.decodeHexString(sizeHint)
				if derr != nil {
					return JSONRPCHexResult{}, derr
				}
				res.Result = data
				res.ResultIsString = true
			} else {
				if err = s.skipValue(c); err != nil {
					return res, err
				}
			}
		case "error":
			raw := make([]byte, 0, 256)
			if raw, err = s.captureValue(c, raw, hexStreamAuxLimit); err != nil {
				return res, err
			}
			if len(raw) > 0 && string(raw) != "null" {
				res.Error = json.RawMessage(raw)
				// an error makes any already decoded result meaningless
				res.Result = nil
				res.ResultIsString = false
			}
		default:
			if err = s.skipValue(c); err != nil {
				return res, err
			}
		}
	}
}

// jsonStream is a minimal pull reader over a JSON document. It never buffers
// more than one fixed-size chunk of input.
type jsonStream struct {
	r   io.Reader
	buf []byte
	off int
	end int
	eof bool
}

// fill makes sure at least one byte is available in the buffer. A short read is
// retried; the end of the document is reported as io.ErrUnexpectedEOF because
// every call site is in the middle of a JSON value (a truncated body - dropped
// connection, backend restart - must never look like a successfully parsed
// response).
func (s *jsonStream) fill() error {
	if s.off < s.end {
		return nil
	}
	for {
		if s.eof {
			return io.ErrUnexpectedEOF
		}
		n, err := s.r.Read(s.buf)
		if n > 0 {
			s.off, s.end = 0, n
			if err == io.EOF {
				s.eof = true
			}
			return nil
		}
		if err != nil {
			if err == io.EOF {
				s.eof = true
				return io.ErrUnexpectedEOF
			}
			return err
		}
	}
}

func (s *jsonStream) readByte() (byte, error) {
	if err := s.fill(); err != nil {
		return 0, err
	}
	c := s.buf[s.off]
	s.off++
	return c, nil
}

// unreadByte pushes back the byte returned by the immediately preceding
// successful readByte. fill always leaves off >= 1 after a read, so this is safe.
func (s *jsonStream) unreadByte() {
	s.off--
}

func (s *jsonStream) nextNonSpace() (byte, error) {
	for {
		c, err := s.readByte()
		if err != nil {
			return 0, err
		}
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		}
		return c, nil
	}
}

// readStringBody consumes a JSON string whose opening quote is already read and
// returns up to limit bytes of it verbatim (escape sequences are kept as-is,
// which is enough for the plain ASCII member names of a JSON-RPC response).
func (s *jsonStream) readStringBody(limit int) ([]byte, error) {
	out := make([]byte, 0, 16)
	for {
		c, err := s.readByte()
		if err != nil {
			return nil, err
		}
		if c == '"' {
			return out, nil
		}
		if len(out) < limit {
			out = append(out, c)
		}
		if c == '\\' {
			e, err := s.readByte()
			if err != nil {
				return nil, err
			}
			if len(out) < limit {
				out = append(out, e)
			}
		}
	}
}

// decodeHexString consumes a JSON string whose opening quote is already read,
// converting it from hex to bytes on the fly.
func (s *jsonStream) decodeHexString(sizeHint int) ([]byte, error) {
	c := 0
	if sizeHint > 0 {
		// +8 so an off-by-a-few hint does not trigger a reallocation
		c = sizeHint + 8
	}
	dst := make([]byte, 0, c)
	var hi byte
	haveHi := false
	for {
		if err := s.fill(); err != nil {
			return nil, err
		}
		chunk := s.buf[s.off:s.end]
		// Reserve room for this chunk in one go, so the inner loop never has to
		// reallocate and the buffer doubles instead of creeping up by the ~1.25x
		// factor the runtime uses for large appends (which would copy the block
		// dozens of times when no size hint is available). Only the hex part of
		// the chunk is counted, so an exact size hint is never overshot by the
		// trailing members of the JSON envelope.
		hexLen := len(chunk)
		if q := bytes.IndexByte(chunk, '"'); q >= 0 {
			hexLen = q
		}
		dst = reserve(dst, (hexLen+1)/2)

		i := 0
		// complete a nibble left over from the previous chunk
		if haveHi && hexLen > 0 {
			v := hexNibbles[chunk[0]]
			if v == 0xff {
				return nil, invalidHexError(chunk[0], 2*len(dst)+1)
			}
			dst = append(dst, hi<<4|v)
			haveHi = false
			i = 1
		}
		// bulk-convert whole hex pairs; the destination is pre-sized above, so
		// this is a plain indexed write with no append bookkeeping
		if n := hexLen - i; n >= 2 {
			n -= n & 1
			src := chunk[i : i+n]
			w := len(dst)
			out := dst[w : w+n/2]
			for j := 0; j < len(out); j++ {
				v1 := hexNibbles[src[2*j]]
				v2 := hexNibbles[src[2*j+1]]
				// valid nibbles are <= 0x0f, the invalid marker is 0xff, so a
				// single OR catches either half being invalid
				if v1|v2 > 0x0f {
					if v1 == 0xff {
						return nil, invalidHexError(src[2*j], 2*(w+j))
					}
					return nil, invalidHexError(src[2*j+1], 2*(w+j)+1)
				}
				out[j] = v1<<4 | v2
			}
			dst = dst[:w+n/2]
			i += n
		}
		// a lone hex character at the end of the chunk carries over
		if i < hexLen {
			v := hexNibbles[chunk[i]]
			if v == 0xff {
				return nil, invalidHexError(chunk[i], 2*len(dst))
			}
			hi = v
			haveHi = true
			i++
		}
		if hexLen < len(chunk) {
			// chunk[hexLen] is the closing quote found by IndexByte above
			s.off += hexLen + 1
			if haveHi {
				return nil, errors.Errorf("invalid hex result, odd number of hex characters (%d bytes decoded)", len(dst))
			}
			return dst, nil
		}
		s.off = s.end
	}
}

func invalidHexError(b byte, hexOffset int) error {
	return errors.Errorf("invalid hex result, character %q at hex offset %d", string(b), hexOffset)
}

// reserve makes sure dst can take n more bytes without reallocating, doubling
// its capacity when it cannot.
func reserve(dst []byte, n int) []byte {
	if cap(dst)-len(dst) >= n {
		return dst
	}
	c := cap(dst)
	if c < hexStreamDefaultCap {
		c = hexStreamDefaultCap
	}
	for c-len(dst) < n {
		c *= 2
	}
	grown := make([]byte, len(dst), c)
	copy(grown, dst)
	return grown
}

// skipValue consumes one JSON value whose first byte c is already read.
func (s *jsonStream) skipValue(c byte) error {
	_, err := s.captureValue(c, nil, 0)
	return err
}

// captureValue consumes one JSON value whose first byte c is already read and
// appends its raw bytes to out, stopping the append (but not the consumption)
// once out reaches limit. Separators inside containers are copied verbatim,
// which is enough to reproduce a well-formed value; the result is handed to
// encoding/json by the caller, which is what actually validates it.
func (s *jsonStream) captureValue(c byte, out []byte, limit int) ([]byte, error) {
	switch c {
	case '{', '[':
		closing := byte('}')
		if c == '[' {
			closing = ']'
		}
		out = appendLimited(out, limit, c)
		for {
			n, err := s.nextNonSpace()
			if err != nil {
				return out, err
			}
			if n == closing {
				return appendLimited(out, limit, n), nil
			}
			if n == ',' || n == ':' {
				out = appendLimited(out, limit, n)
				continue
			}
			if out, err = s.captureValue(n, out, limit); err != nil {
				return out, err
			}
		}
	case '"':
		out = appendLimited(out, limit, '"')
		for {
			b, err := s.readByte()
			if err != nil {
				return out, err
			}
			out = appendLimited(out, limit, b)
			if b == '\\' {
				e, err := s.readByte()
				if err != nil {
					return out, err
				}
				out = appendLimited(out, limit, e)
				continue
			}
			if b == '"' {
				return out, nil
			}
		}
	default:
		// number, true, false or null
		out = appendLimited(out, limit, c)
		for {
			b, err := s.readByte()
			if err != nil {
				return out, err
			}
			if isScalarByte(b) {
				out = appendLimited(out, limit, b)
				continue
			}
			s.unreadByte()
			return out, nil
		}
	}
}

func appendLimited(out []byte, limit int, b byte) []byte {
	if out == nil || len(out) >= limit {
		return out
	}
	return append(out, b)
}

func isScalarByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b == '+' || b == '-' || b == '.':
		return true
	}
	return false
}
