// Package deliveryv1 implements only Delivery Protocol v1 frame syntax.
// Authentication, request state, ordering, and persistence belong to callers.
package deliveryv1

import (
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	Send          byte = 0x10
	Sync          byte = 0x11
	Ack           byte = 0x90
	Item          byte = 0x91
	End           byte = 0x92
	Changed       byte = 0x93
	Failure       byte = 0xff
	Rejected      byte = 0x01
	Conflict      byte = 0x02
	CursorInvalid byte = 0x03
	Retryable     byte = 0x04
	MaxCharacters      = 500
	MaxTextBytes       = 2000
	MaxBody            = 2041
)

var ErrInvalid = errors.New("invalid Delivery Protocol v1 frame")

// Frame holds the fields of one operation. Only fields named in that operation's
// layout are encoded; unused fields are ignored. Zero is not an ID or sequence.
// Signed int64 deliberately restricts wire u64 values to Java ME's positive range.
type Frame struct {
	Kind                  byte
	ClientSeq, MessageID  int64
	Recipient, Sender     uint32
	After, Through        int64
	Limit                 byte
	Cursor, WriterID      int64
	Next, Tail, Reference int64
	RequestOp, Code       byte
	Text                  string
}

func validID(id uint32) bool { return id >= 100000 && id <= 999999999 }
func validText(s string) bool {
	return len(s) > 0 && len(s) <= MaxTextBytes && utf8.ValidString(s) && utf8.RuneCountInString(s) <= MaxCharacters
}

func (f Frame) bodySize() (int, error) {
	switch f.Kind {
	case Send:
		if f.ClientSeq > 0 && validID(f.Recipient) && validText(f.Text) {
			return 13 + len(f.Text), nil
		}
	case Ack:
		if f.ClientSeq > 0 && f.MessageID > 0 {
			return 17, nil
		}
	case Sync:
		if f.After >= 0 && f.Through >= 0 && (f.Through == 0 || f.After <= f.Through) && f.Limit >= 1 && f.Limit <= 8 {
			return 18, nil
		}
	case Item:
		if f.Cursor > 0 && f.MessageID > 0 && validID(f.Sender) && validID(f.Recipient) && f.WriterID > 0 && f.ClientSeq > 0 && validText(f.Text) {
			return 41 + len(f.Text), nil
		}
	case End:
		if f.Next >= 0 && f.Through >= f.Next && (f.Next > 0 || f.Through == 0) {
			return 17, nil
		}
	case Changed:
		if f.Tail >= 0 {
			return 9, nil
		}
	case Failure:
		if f.Code >= Rejected && f.Code <= Retryable && ((f.RequestOp == Send && f.Reference > 0) || (f.RequestOp == Sync && f.Reference >= 0)) {
			return 11, nil
		}
	}
	return 0, ErrInvalid
}

// Marshal returns one complete length-prefixed frame, at most MaxBody+2 bytes.
func (f Frame) Marshal() ([]byte, error) {
	n, err := f.bodySize()
	if err != nil {
		return nil, err
	}
	b := make([]byte, n+2)
	binary.BigEndian.PutUint16(b, uint16(n))
	b[2] = f.Kind
	p := 3
	u64 := func(v int64) { binary.BigEndian.PutUint64(b[p:p+8], uint64(v)); p += 8 }
	u32 := func(v uint32) { binary.BigEndian.PutUint32(b[p:p+4], v); p += 4 }
	switch f.Kind {
	case Send:
		u64(f.ClientSeq)
		u32(f.Recipient)
	case Ack:
		u64(f.ClientSeq)
		u64(f.MessageID)
	case Sync:
		u64(f.After)
		u64(f.Through)
		b[p] = f.Limit
	case Item:
		u64(f.Cursor)
		u64(f.MessageID)
		u32(f.Sender)
		u32(f.Recipient)
		u64(f.WriterID)
		u64(f.ClientSeq)
	case End:
		u64(f.Next)
		u64(f.Through)
	case Changed:
		u64(f.Tail)
	case Failure:
		b[p] = f.RequestOp
		p++
		u64(f.Reference)
		b[p] = f.Code
	}
	if f.Kind == Send || f.Kind == Item {
		copy(b[p:], f.Text)
	}
	return b, nil
}

// Decode requires exactly one complete frame. Use Read for concatenated streams.
func Decode(b []byte) (Frame, error) {
	if len(b) < 2 {
		return Frame{}, ErrInvalid
	}
	n := int(binary.BigEndian.Uint16(b))
	if n < 9 || n > MaxBody || len(b) != n+2 {
		return Frame{}, ErrInvalid
	}
	f := Frame{Kind: b[2]}
	// Check the operation length before reading any operation fields.
	validSize := false
	switch f.Kind {
	case Send:
		validSize = n >= 14 && n <= 2013
	case Ack, End:
		validSize = n == 17
	case Sync:
		validSize = n == 18
	case Item:
		validSize = n >= 42 && n <= MaxBody
	case Changed:
		validSize = n == 9
	case Failure:
		validSize = n == 11
	}
	if !validSize {
		return Frame{}, ErrInvalid
	}
	p := 3
	u64 := func() int64 { v := int64(binary.BigEndian.Uint64(b[p : p+8])); p += 8; return v }
	u32 := func() uint32 { v := binary.BigEndian.Uint32(b[p : p+4]); p += 4; return v }
	switch f.Kind {
	case Send:
		f.ClientSeq = u64()
		f.Recipient = u32()
	case Ack:
		f.ClientSeq = u64()
		f.MessageID = u64()
	case Sync:
		f.After = u64()
		f.Through = u64()
		f.Limit = b[p]
	case Item:
		f.Cursor = u64()
		f.MessageID = u64()
		f.Sender = u32()
		f.Recipient = u32()
		f.WriterID = u64()
		f.ClientSeq = u64()
	case End:
		f.Next = u64()
		f.Through = u64()
	case Changed:
		f.Tail = u64()
	case Failure:
		f.RequestOp = b[p]
		p++
		f.Reference = u64()
		f.Code = b[p]
	}
	if f.Kind == Send || f.Kind == Item {
		f.Text = string(b[p:])
	}
	if _, err := f.bodySize(); err != nil {
		return Frame{}, err
	}
	return f, nil
}

// Read consumes one frame without reading into the next. Allocate only after
// validating the prefix; a malformed frame is not a recoverable stream boundary.
func Read(r io.Reader) (Frame, error) {
	var prefix [2]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return Frame{}, err
	}
	n := int(binary.BigEndian.Uint16(prefix[:]))
	if n < 9 || n > MaxBody {
		return Frame{}, ErrInvalid
	}
	b := make([]byte, n+2)
	copy(b, prefix[:])
	if _, err := io.ReadFull(r, b[2:]); err != nil {
		return Frame{}, err
	}
	return Decode(b)
}

// Write finishes partial writes and does not flush or close caller-owned streams.
func Write(w io.Writer, f Frame) error {
	b, err := f.Marshal()
	if err != nil {
		return err
	}
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
