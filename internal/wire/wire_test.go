package wire

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestUTF8AndBoundaries(t *testing.T) {
	for _, text := range []string{"hello", "\u041f\u0440\u0438\u0432\u0456\u0442, \u0441\u0432\u0456\u0442!", "Za\u017c\u00f3\u0142\u0107 g\u0119\u015bl\u0105 ja\u017a\u0144", "e\u0301", "\U0001f642", "\x00\n", strings.Repeat("a", 500), strings.Repeat("\U0001f642", 500)} {
		want := Frame{Kind: Send, ID: SecondID, Text: text}
		b, err := want.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		got, err := Read(iotest.OneByteReader(bytes.NewReader(b)))
		if err != nil || got != want {
			t.Fatalf("round-trip mismatch: %v", err)
		}
		if !bytes.Equal(b[7:], []byte(text)) {
			t.Fatal("not standard UTF-8")
		}
	}
	for _, text := range []string{"", strings.Repeat("a", 501), strings.Repeat("\U0001f642", 501), string([]byte{0xc0, 0x80}), string([]byte{0xed, 0xa0, 0x80}), string([]byte{0xf4, 0x90, 0x80, 0x80})} {
		if _, err := (Frame{Kind: Send, ID: SecondID, Text: text}).Marshal(); err == nil {
			t.Fatal("invalid text accepted")
		}
	}
}

func TestCombinedFramesAndPartialWrites(t *testing.T) {
	frames := []Frame{{Kind: Hello, ID: FirstID}, {Kind: Message, ID: SecondID, Text: "one"}, {Kind: Message, ID: FirstID, Text: "two"}, {Kind: Failure, Code: Unavailable}}
	var output shortWriter
	for _, f := range frames {
		if err := Write(&output, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range frames {
		got, err := Read(&output.Buffer)
		if err != nil || got != want {
			t.Fatalf("got %+v: %v", got, err)
		}
	}
	if _, err := Read(&output.Buffer); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
	if err := Write(zeroWriter{}, frames[0]); err != io.ErrShortWrite {
		t.Fatal(err)
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(b []byte) (int, error) {
	if len(b) > 1 {
		b = b[:1]
	}
	return w.Buffer.Write(b)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestMalformedFrames(t *testing.T) {
	valid, _ := (Frame{Kind: Send, ID: SecondID, Text: "hello"}).Marshal()
	invalidUTF8 := append([]byte(nil), valid...)
	invalidUTF8[7] = 0xff
	badID := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(badID[3:7], 42)
	cases := [][]byte{{0}, {0, 0}, {0, 1, 1}, {0xff, 0xff}, {0, 2, 0x99, 0}, {0, 2, Failure, 0}, {0, 2, Hello, 0}, invalidUTF8, badID}
	for i := 2; i < len(valid); i++ {
		cases = append(cases, valid[:i])
	}
	for _, b := range cases {
		if _, err := Read(bytes.NewReader(b)); err == nil {
			t.Fatalf("accepted malformed %x", b)
		}
	}
}

func TestOverCapDoesNotReadBody(t *testing.T) {
	r := bytes.NewReader([]byte{0xff, 0xff, 1, 2, 3})
	if _, err := Read(r); err != ErrInvalid || r.Len() != 3 {
		t.Fatal("oversized frame body was consumed")
	}
	allocs := testing.AllocsPerRun(100, func() { Read(bytes.NewReader([]byte{0xff, 0xff})) })
	if allocs > 3 {
		t.Fatalf("unexpected allocations for rejected length: %.0f", allocs)
	}
}

func FuzzRead(f *testing.F) {
	f.Add([]byte{0, 5, 1, 0, 1, 0x86, 0xa1})
	f.Add([]byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		frame, err := Read(bytes.NewReader(b))
		if err != nil {
			return
		}
		encoded, err := frame.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > MaxBody+2 {
			t.Fatal("frame exceeds cap")
		}
		again, err := Read(bytes.NewReader(encoded))
		if err != nil || again != frame {
			t.Fatal("unstable frame")
		}
	})
}

func BenchmarkFrameRoundTrip(b *testing.B) {
	f := Frame{Kind: Send, ID: SecondID, Text: "ASCII / \u041f\u0440\u0438\u0432\u0456\u0442 / Za\u017c\u00f3\u0142\u0107 / \U0001f642"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		data, _ := f.Marshal()
		if _, err := Read(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}
