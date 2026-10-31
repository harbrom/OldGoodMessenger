// Package wire implements the experimental First Contact protocol, not the production protocol.
package wire

import (
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	Hello         byte   = 0x01
	Send          byte   = 0x02
	Ready         byte   = 0x81
	Message       byte   = 0x82
	Failure       byte   = 0xff
	BadRequest    byte   = 1
	Unavailable   byte   = 2
	IdentityBusy  byte   = 3
	MaxCharacters        = 500 // Unicode scalar values for this experiment only.
	MaxBody              = 5 + 4*MaxCharacters
	FirstID       uint32 = 100001
	SecondID      uint32 = 100002
)

var ErrInvalid = errors.New("invalid First Contact frame")

type Frame struct {
	Kind byte
	ID   uint32
	Text string
	Code byte
}

func ValidID(id uint32) bool { return id == FirstID || id == SecondID }

func ValidText(text string) bool {
	return len(text) > 0 && len(text) <= 4*MaxCharacters && utf8.ValidString(text) && utf8.RuneCountInString(text) <= MaxCharacters
}

func (f Frame) Marshal() ([]byte, error) {
	n := 5
	switch f.Kind {
	case Hello, Ready:
		if !ValidID(f.ID) || f.Text != "" {
			return nil, ErrInvalid
		}
	case Send, Message:
		if !ValidID(f.ID) || !ValidText(f.Text) {
			return nil, ErrInvalid
		}
		n += len(f.Text)
	case Failure:
		if f.Code < BadRequest || f.Code > IdentityBusy {
			return nil, ErrInvalid
		}
		n = 2
	default:
		return nil, ErrInvalid
	}
	b := make([]byte, n+2)
	binary.BigEndian.PutUint16(b, uint16(n))
	b[2] = f.Kind
	if f.Kind == Failure {
		b[3] = f.Code
	} else {
		binary.BigEndian.PutUint32(b[3:7], f.ID)
		copy(b[7:], f.Text)
	}
	return b, nil
}

func Read(r io.Reader) (Frame, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	n := int(binary.BigEndian.Uint16(header[:]))
	if n < 2 || n > MaxBody {
		return Frame{}, ErrInvalid
	}
	body := make([]byte, n) // Allocation is bounded before reading the body.
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, err
	}
	f := Frame{Kind: body[0]}
	switch f.Kind {
	case Hello, Ready:
		if n != 5 {
			return Frame{}, ErrInvalid
		}
	case Send, Message:
		if n < 6 {
			return Frame{}, ErrInvalid
		}
	case Failure:
		if n != 2 || body[1] < BadRequest || body[1] > IdentityBusy {
			return Frame{}, ErrInvalid
		}
		f.Code = body[1]
		return f, nil
	default:
		return Frame{}, ErrInvalid
	}
	f.ID = binary.BigEndian.Uint32(body[1:5])
	if !ValidID(f.ID) {
		return Frame{}, ErrInvalid
	}
	if f.Kind == Send || f.Kind == Message {
		f.Text = string(body[5:])
		if !ValidText(f.Text) {
			return Frame{}, ErrInvalid
		}
	}
	return f, nil
}

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
		if n <= 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
