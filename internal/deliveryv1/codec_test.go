package deliveryv1

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf16"
)

// This parser reads checked-in platform-independent expectations, not codec output.
func fixtureFrame(t *testing.T, spec string) Frame {
	t.Helper()
	parts := strings.Split(spec, ";")
	op, err := strconv.ParseUint(parts[0], 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	f := Frame{Kind: byte(op)}
	for _, field := range parts[1:] {
		pair := strings.SplitN(field, "=", 2)
		if len(pair) != 2 {
			t.Fatal("bad fixture field")
		}
		key, value := pair[0], pair[1]
		if key == "text" {
			f.Text = fixtureText(t, value)
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		switch key {
		case "seq":
			f.ClientSeq = n
		case "to":
			f.Recipient = uint32(n)
		case "from":
			f.Sender = uint32(n)
		case "id":
			f.MessageID = n
		case "after":
			f.After = n
		case "through":
			f.Through = n
		case "limit":
			f.Limit = byte(n)
		case "cursor":
			f.Cursor = n
		case "writer":
			f.WriterID = n
		case "next":
			f.Next = n
		case "tail":
			f.Tail = n
		case "req":
			f.RequestOp = byte(n)
		case "ref":
			f.Reference = n
		case "code":
			f.Code = byte(n)
		default:
			t.Fatalf("unknown fixture field %q", key)
		}
	}
	return f
}

func fixtureText(t *testing.T, text string) string {
	t.Helper()
	if len(text)%4 != 0 {
		t.Fatal("invalid UTF-16 fixture")
	}
	var out strings.Builder
	for p := 0; p < len(text); p += 4 {
		n, err := strconv.ParseUint(text[p:p+4], 16, 16)
		if err != nil {
			t.Fatal(err)
		}
		r := rune(n)
		if r >= 0xd800 && r <= 0xdbff && p+8 <= len(text) {
			low, err := strconv.ParseUint(text[p+4:p+8], 16, 16)
			if err != nil {
				t.Fatal(err)
			}
			if low >= 0xdc00 && low <= 0xdfff {
				out.WriteRune(utf16.DecodeRune(r, rune(low)))
				p += 4
				continue
			}
		}
		if r >= 0xd800 && r <= 0xdfff {
			// Go strings cannot contain a surrogate scalar. Preserve its invalid
			// UTF-8 bytes rather than silently replacing it with valid U+FFFD.
			out.Write([]byte{byte(0xe0 | (r >> 12)), byte(0x80 | ((r >> 6) & 63)), byte(0x80 | (r & 63))})
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func TestSharedFixtures(t *testing.T) {
	data, err := os.ReadFile("../../testdata/delivery-v1/fixtures.txt")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(line, "\r"), "|")
		if len(parts) != 4 {
			t.Fatal("invalid fixture row")
		}
		name, mode, spec := parts[0], parts[1], parts[2]
		counts[mode]++
		t.Run(name, func(t *testing.T) {
			var wire []byte
			if parts[3] != "-" {
				wire, err = hex.DecodeString(parts[3])
				if err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "valid":
				want := fixtureFrame(t, spec)
				got, err := Decode(wire)
				if err != nil || got != want {
					t.Fatalf("decoded %+v, want %+v: %v", got, want, err)
				}
				encoded, err := want.Marshal()
				if err != nil || !bytes.Equal(encoded, wire) {
					t.Fatalf("encoding differs from authoritative bytes: %v", err)
				}
				got, err = Read(iotest.OneByteReader(bytes.NewReader(wire)))
				if err != nil || got != want {
					t.Fatalf("split frame: %v", err)
				}
				var output partialWriter
				if err := Write(&output, want); err != nil || !bytes.Equal(output.Bytes(), wire) {
					t.Fatalf("partial write: %v", err)
				}
				for p := 0; p < len(wire); p++ {
					if _, err := Decode(wire[:p]); err == nil {
						t.Fatalf("accepted truncated frame at %d", p)
					}
					if _, err := Read(bytes.NewReader(wire[:p])); err == nil {
						t.Fatalf("stream accepted truncation at %d", p)
					}
				}
			case "invalid":
				if _, err := Decode(wire); err == nil {
					t.Fatal("invalid wire accepted")
				}
				if _, err := Read(bytes.NewReader(wire)); err == nil {
					t.Fatal("invalid stream accepted")
				}
				if spec != "-" {
					if _, err := fixtureFrame(t, spec).Marshal(); err == nil {
						t.Fatal("invalid fields encoded")
					}
				}
			case "encode-invalid":
				if _, err := fixtureFrame(t, spec).Marshal(); err == nil {
					t.Fatal("invalid Unicode encoded")
				}
			case "stream":
				if _, err := Decode(wire); err == nil {
					t.Fatal("concatenation accepted as one frame")
				}
				input := bytes.NewReader(wire)
				for _, frame := range strings.Split(spec, "/") {
					got, err := Read(iotest.OneByteReader(input))
					if err != nil || got != fixtureFrame(t, frame) {
						t.Fatalf("stream frame: %+v: %v", got, err)
					}
				}
				if input.Len() != 0 {
					t.Fatal("unexpected stream remainder")
				}
				if _, err := Read(input); err != io.EOF {
					t.Fatalf("expected EOF: %v", err)
				}
			default:
				t.Fatal("unknown fixture mode")
			}
		})
	}
	if counts["valid"] != 26 || counts["invalid"] != 61 || counts["encode-invalid"] != 4 || counts["stream"] != 2 {
		t.Fatalf("fixture coverage changed: %v", counts)
	}
}

type partialWriter struct{ bytes.Buffer }

func (w *partialWriter) Write(b []byte) (int, error) {
	if len(b) > 2 {
		b = b[:2]
	}
	return w.Buffer.Write(b)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestBoundsAndIOFailures(t *testing.T) {
	for _, prefix := range [][]byte{{0, 0}, {0, 8}, {7, 250}, {255, 255}} {
		input := bytes.NewReader(append(append([]byte{}, prefix...), 1, 2, 3))
		if _, err := Read(input); !errors.Is(err, ErrInvalid) || input.Len() != 3 {
			t.Fatalf("invalid prefix consumed body: %v", err)
		}
	}
	want := Frame{Kind: Changed}
	if err := Write(zeroWriter{}, want); err != io.ErrShortWrite {
		t.Fatal(err)
	}
	if err := Write(failingWriter{}, want); err != io.ErrClosedPipe {
		t.Fatal(err)
	}
	if _, err := Read(iotest.ErrReader(io.ErrClosedPipe)); err != io.ErrClosedPipe {
		t.Fatal(err)
	}
	if _, err := Read(bytes.NewReader([]byte{0})); err != io.ErrUnexpectedEOF {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Write(&output, Frame{}); err != ErrInvalid || output.Len() != 0 {
		t.Fatal("invalid frame wrote bytes")
	}
}
