package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryloopback"
	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
	"ogm.local/firstcontact/internal/sqlitestore"
)

const maxSequence int64 = 1<<63 - 1

type client struct {
	name    string
	account uint32
	writer  int64
	seq     int64
	pending *deliveryv1.Frame
	peer    *connection
}

type connection struct {
	conn   net.Conn
	cancel context.CancelFunc
	done   chan error
}

type console struct {
	store    *sqlitestore.Store
	acceptor *acceptance.Acceptor
	history  *historysync.Service
	clients  [2]*client
	closed   bool
	closeErr error
}

func writerID() (int64, error) {
	for {
		var bytes [8]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return 0, fmt.Errorf("generate test writer: %w", err)
		}
		if id := int64(binary.BigEndian.Uint64(bytes[:]) & uint64(maxSequence)); id != 0 {
			return id, nil
		}
	}
}

func openConsole(path string) (_ *console, err error) {
	s, err := sqlitestore.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	c := &console{store: s}
	defer func() {
		if err != nil {
			err = errors.Join(err, c.close(io.Discard))
		}
	}()
	c.acceptor, err = acceptance.New(s, func(_ acceptance.TrustedContext, r acceptance.Request) bool {
		return r.Recipient == 100001 || r.Recipient == 100002
	})
	if err != nil {
		return nil, err
	}
	c.history, err = historysync.New(s)
	if err != nil {
		return nil, err
	}
	for i, name := range []string{"alice", "bob"} {
		id, err := writerID()
		if err != nil {
			return nil, err
		}
		for i == 1 && id == c.clients[0].writer {
			id, err = writerID()
			if err != nil {
				return nil, err
			}
		}
		cl := &client{name: name, account: uint32(100001 + i), writer: id}
		c.clients[i] = cl
		if err := c.connect(cl); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *console) connect(cl *client) error {
	if c.closed {
		return errors.New("console is closed")
	}
	if cl.peer != nil {
		return nil
	}
	// One temporary listener per account; close it immediately after this pair.
	// Identity is tied to this Go call, never selected by any bytes from the peer.
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for %s: %w", cl.name, err)
	}
	defer l.Close()
	if err := l.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	socket, err := net.DialTimeout("tcp4", l.Addr().String(), 3*time.Second)
	if err != nil {
		return fmt.Errorf("dial for %s: %w", cl.name, err)
	}
	server, err := l.Accept()
	if err != nil {
		socket.Close()
		return fmt.Errorf("accept for %s: %w", cl.name, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &connection{conn: socket, cancel: cancel, done: make(chan error, 1)}
	cl.peer = p
	go func() {
		p.done <- deliveryloopback.Handle(ctx, server, acceptance.TrustedContext{AccountID: cl.account, WriterID: cl.writer}, c.acceptor, c.history)
	}()
	return nil
}

func disconnect(cl *client) error {
	if cl.peer == nil {
		return nil
	}
	p := cl.peer
	cl.peer = nil
	p.cancel()
	p.conn.Close()
	// Join before closing SQLite: an already-started acceptance may still commit.
	err := <-p.done
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (c *console) close(output io.Writer) error {
	if c.closed {
		return c.closeErr
	}
	c.closed = true
	for _, cl := range c.clients {
		if cl == nil {
			continue
		}
		if cl.pending != nil {
			fmt.Fprintf(output, "WARNING: %s has uncertain SEND seq=%d to=%d text=%q; no durable outbox. Exit loses its retry state; acceptance may already have committed.\n", cl.name, cl.pending.ClientSeq, cl.pending.Recipient, cl.pending.Text)
		}
		c.closeErr = errors.Join(c.closeErr, disconnect(cl))
	}
	c.closeErr = errors.Join(c.closeErr, c.store.Close())
	return c.closeErr
}

func (c *console) lookup(name string) (*client, error) {
	for _, cl := range c.clients {
		if cl.name == name {
			return cl, nil
		}
	}
	return nil, fmt.Errorf("unknown account %q; use alice or bob", name)
}

// Consume one separator after each token. After the recipient, the rest is exact
// message text, including additional leading/trailing spaces; never normalize it.
func token(line string) (string, string) {
	line = strings.TrimLeftFunc(line, unicode.IsSpace)
	if i := strings.IndexFunc(line, unicode.IsSpace); i >= 0 {
		_, width := utf8.DecodeRuneInString(line[i:])
		return line[:i], line[i+width:]
	}
	return line, ""
}

func (c *console) command(line string, output io.Writer) bool {
	verb, rest := token(line)
	if verb == "" {
		return false
	}
	if (verb == "help" || verb == "quit") && strings.TrimSpace(rest) == "" {
		if verb == "quit" {
			return true
		}
		fmt.Fprintln(output, "help | send <alice|bob> <alice|bob> <text> | sync <alice|bob> | retry <alice|bob> | quit")
		return false
	}
	name, rest := token(rest)
	cl, err := c.lookup(name)
	if err == nil {
		switch verb {
		case "send":
			var recipient *client
			to, text := token(rest)
			recipient, err = c.lookup(to)
			if err == nil {
				err = c.send(cl, recipient, text, output)
			}
		case "retry", "sync":
			if strings.TrimSpace(rest) != "" {
				err = errors.New("unexpected command arguments")
			} else if verb == "retry" {
				if cl.pending == nil {
					err = errors.New("no uncertain SEND to retry")
				} else {
					err = c.submit(cl, output)
				}
			} else {
				err = c.replay(cl, output)
			}
		default:
			err = errors.New("unknown command; type help")
		}
	}
	if err != nil {
		fmt.Fprintln(output, "Error:", err)
	}
	return false
}

func (c *console) send(cl, to *client, text string, output io.Writer) error {
	if cl.pending != nil {
		return errors.New("uncertain SEND remains; use retry for this account before a new send")
	}
	if !deliveryv1.ValidText(text) {
		return errors.New("text must be valid UTF-8, 1..500 Unicode scalars and at most 2000 bytes")
	}
	if cl.seq == maxSequence {
		return errors.New("client sequence exhausted; restart with a fresh test writer")
	}
	cl.seq++
	cl.pending = &deliveryv1.Frame{Kind: deliveryv1.Send, ClientSeq: cl.seq, Recipient: to.account, Text: text}
	return c.submit(cl, output)
}

func (c *console) request(cl *client, frame deliveryv1.Frame) error {
	if err := c.connect(cl); err != nil {
		return err
	}
	if err := cl.peer.conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return deliveryv1.Write(cl.peer.conn, frame)
}

func (c *console) submit(cl *client, output io.Writer) (err error) {
	r := *cl.pending
	fmt.Fprintf(output, "SEND %s (%d) -> %d writer=%d seq=%d text=%q\n", cl.name, cl.account, r.Recipient, cl.writer, r.ClientSeq, r.Text)
	defer func() {
		if err != nil {
			err = errors.Join(err, disconnect(cl))
			fmt.Fprintf(output, "UNCERTAIN %s seq=%d retained exactly; retry %s\n", cl.name, r.ClientSeq, cl.name)
		}
	}()
	if err = c.request(cl, r); err != nil {
		return err
	}
	f, err := deliveryv1.Read(cl.peer.conn)
	if err != nil {
		return fmt.Errorf("read SEND response: %w", err)
	}
	switch {
	case f.Kind == deliveryv1.Ack && f.ClientSeq == r.ClientSeq:
		cl.pending = nil
		fmt.Fprintf(output, "ACK %s seq=%d message_id=%d (storage accepted, not a delivery receipt)\n", cl.name, f.ClientSeq, f.MessageID)
	case f.Kind == deliveryv1.Failure && f.RequestOp == deliveryv1.Send && f.Reference == r.ClientSeq:
		switch f.Code {
		case deliveryv1.Rejected, deliveryv1.Conflict:
			cl.pending = nil
			fmt.Fprintf(output, "%s %s seq=%d (not ACK)\n", errorName(f.Code), cl.name, r.ClientSeq)
		case deliveryv1.Retryable:
			fmt.Fprintf(output, "RETRYABLE %s seq=%d (not ACK); exact pending SEND retained, use retry %s\n", cl.name, r.ClientSeq, cl.name)
		default:
			return errors.New("unexpected SEND error code")
		}
	default:
		return errors.New("unexpected or mismatched SEND response")
	}
	return nil
}

func errorName(code byte) string {
	switch code {
	case deliveryv1.Rejected:
		return "REJECTED"
	case deliveryv1.Conflict:
		return "CONFLICT"
	case deliveryv1.CursorInvalid:
		return "CURSOR_INVALID"
	default:
		return "RETRYABLE"
	}
}

func (c *console) replay(cl *client, output io.Writer) (err error) {
	defer func() {
		if err != nil {
			err = errors.Join(err, disconnect(cl))
		}
	}()
	// Manual replay only. Retain no history cache or persistent device checkpoint.
	var after, snapshot, lastID int64
	for {
		req := deliveryv1.Frame{Kind: deliveryv1.Sync, After: after, Through: snapshot, Limit: 8}
		if err := c.request(cl, req); err != nil {
			return fmt.Errorf("SYNC %s: %w", cl.name, err)
		}
		count, lastCursor := 0, after
		for {
			f, err := deliveryv1.Read(cl.peer.conn)
			if err != nil {
				return fmt.Errorf("read SYNC response: %w", err)
			}
			switch f.Kind {
			case deliveryv1.Item:
				if count >= 8 || f.Cursor <= lastCursor || f.MessageID <= lastID ||
					(f.Sender != cl.account && f.Recipient != cl.account) || (snapshot != 0 && f.Cursor > snapshot) {
					return errors.New("invalid history ITEM ordering, membership or page size")
				}
				count++
				lastCursor, lastID = f.Cursor, f.MessageID
				fmt.Fprintf(output, "ITEM %s cursor=%d message_id=%d sender=%d recipient=%d writer=%d seq=%d text=%q\n", cl.name, f.Cursor, f.MessageID, f.Sender, f.Recipient, f.WriterID, f.ClientSeq, f.Text)
			case deliveryv1.End:
				if f.Through < lastCursor || f.Next < after || (snapshot != 0 && f.Through != snapshot) ||
					(f.Next < f.Through && (count != 8 || f.Next != lastCursor)) {
					return errors.New("invalid history END or changed snapshot")
				}
				fmt.Fprintf(output, "END %s next=%d through=%d items=%d\n", cl.name, f.Next, f.Through, count)
				after, snapshot = f.Next, f.Through
			case deliveryv1.Failure:
				if f.RequestOp != deliveryv1.Sync || f.Reference != req.After {
					return errors.New("mismatched SYNC error")
				}
				return fmt.Errorf("SYNC %s: %s (no complete replay)", cl.name, errorName(f.Code))
			default:
				return errors.New("unexpected history operation")
			}
			if f.Kind == deliveryv1.End {
				break
			}
		}
		if after == snapshot {
			fmt.Fprintf(output, "Replay complete for %s through=%d; no persistent client checkpoint.\n", cl.name, snapshot)
			return nil
		}
	}
}
