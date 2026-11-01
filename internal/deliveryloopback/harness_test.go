package deliveryloopback

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
	"ogm.local/firstcontact/internal/sqlitestore"
)

var alice = acceptance.TrustedContext{AccountID: 100001, WriterID: 11}
var bob = acceptance.TrustedContext{AccountID: 100002, WriterID: 22}
var nobody = acceptance.TrustedContext{AccountID: 100003, WriterID: 33}
var errInjected = errors.New("test-only sensitive database detail: must not reach the wire")

func policy(_ acceptance.TrustedContext, req acceptance.Request) bool {
	return req.Recipient == alice.AccountID || req.Recipient == bob.AccountID
}
func database(t *testing.T, path string) *sqlitestore.Store {
	t.Helper()
	s, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func services(t *testing.T, store acceptance.Store, reader historysync.Store) (*acceptance.Acceptor, *historysync.Service) {
	t.Helper()
	a, err := acceptance.New(store, policy)
	if err != nil {
		t.Fatal(err)
	}
	h, err := historysync.New(reader)
	if err != nil {
		t.Fatal(err)
	}
	return a, h
}
func fresh(t *testing.T) (*sqlitestore.Store, *acceptance.Acceptor, *historysync.Service) {
	t.Helper()
	s := database(t, filepath.Join(t.TempDir(), "loopback.sqlite"))
	a, h := services(t, s, s)
	return s, a, h
}

type peer struct {
	client, server net.Conn
	cancel         context.CancelFunc
	done           chan struct{}
	err            error
}

func connect(t *testing.T, who acceptance.TrustedContext, a *acceptance.Acceptor, h *historysync.Service, wrap func(net.Conn) net.Conn) *peer {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	client, err := net.DialTimeout("tcp4", l.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	server, err := l.Accept()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		client.Close()
		server.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &peer{client: client, server: server, cancel: cancel, done: make(chan struct{})}
	if wrap != nil {
		p.server = wrap(server)
	}
	if err := client.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		client.Close()
		server.Close()
		cancel()
		t.Fatal(err)
	}
	go func() { p.err = Handle(ctx, p.server, who, a, h); close(p.done) }()
	t.Cleanup(func() { p.cancel(); p.client.Close(); p.wait(t) })
	return p
}
func (p *peer) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-p.done:
		return p.err
	case <-time.After(8 * time.Second):
		t.Fatal("handler did not exit/join cleanup")
		return nil
	}
}
func (p *peer) stop(t *testing.T) { t.Helper(); p.cancel(); p.client.Close(); p.wait(t) }
func send(t *testing.T, p *peer, f deliveryv1.Frame) {
	t.Helper()
	if err := deliveryv1.Write(p.client, f); err != nil {
		t.Fatal(err)
	}
}
func receive(t *testing.T, p *peer) deliveryv1.Frame {
	t.Helper()
	f, err := deliveryv1.Read(p.client)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func message(seq int64, to uint32, text string) deliveryv1.Frame {
	return deliveryv1.Frame{Kind: deliveryv1.Send, ClientSeq: seq, Recipient: to, Text: text}
}
func syncRequest(after, through int64, limit byte) deliveryv1.Frame {
	return deliveryv1.Frame{Kind: deliveryv1.Sync, After: after, Through: through, Limit: limit}
}
func wantFrame(t *testing.T, got, want deliveryv1.Frame) {
	t.Helper()
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
func page(t *testing.T, p *peer, req deliveryv1.Frame) []deliveryv1.Frame {
	t.Helper()
	send(t, p, req)
	var frames []deliveryv1.Frame
	for len(frames) <= int(req.Limit) {
		f := receive(t, p)
		frames = append(frames, f)
		if f.Kind == deliveryv1.End {
			return frames
		}
		if f.Kind != deliveryv1.Item {
			t.Fatalf("unexpected page frame %+v", f)
		}
	}
	t.Fatal("unbounded history response")
	return nil
}
func counts(t *testing.T, s *sqlitestore.Store, account uint32, want int) {
	t.Helper()
	if err := s.View(account, func(r historysync.Reader) error {
		tail, err := r.Tail()
		if err != nil {
			return err
		}
		rows, err := r.Records(0, tail, 9)
		if err != nil {
			return err
		}
		if tail != int64(want) || len(rows) != want {
			return errors.New("wrong stored count/cursor")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Wrap real TCP only: force partial I/O and observe/expire deadlines without
// waiting five seconds or relying on TCP packet segmentation or sleeps.
type observedConn struct {
	net.Conn
	maxRead, maxWrite       int
	readDeadlines           chan time.Time
	writeDeadlines          chan time.Time
	expireRead, expireWrite bool
}

func (c *observedConn) Read(b []byte) (int, error) {
	if c.maxRead > 0 && len(b) > c.maxRead {
		b = b[:c.maxRead]
	}
	return c.Conn.Read(b)
}
func (c *observedConn) Write(b []byte) (int, error) {
	if c.maxWrite > 0 && len(b) > c.maxWrite {
		b = b[:c.maxWrite]
	}
	return c.Conn.Write(b)
}
func (c *observedConn) SetReadDeadline(d time.Time) error {
	if c.readDeadlines != nil {
		c.readDeadlines <- d
	}
	if c.expireRead && !d.IsZero() {
		d = time.Now()
	}
	return c.Conn.SetReadDeadline(d)
}
func (c *observedConn) SetWriteDeadline(d time.Time) error {
	if c.writeDeadlines != nil {
		c.writeDeadlines <- d
	}
	if c.expireWrite {
		d = time.Now()
	}
	return c.Conn.SetWriteDeadline(d)
}

type gatedWrite struct {
	net.Conn
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *gatedWrite) Write(b []byte) (n int, err error) {
	first := false
	c.once.Do(func() { first = true })
	if first {
		n, err = c.Conn.Write(b[:1])
		close(c.entered)
		<-c.release
		return n, err
	}
	return c.Conn.Write(b)
}

// All transaction/service failures below are injected only by test adapters.
type commitStore struct {
	base    acceptance.Store
	mode    string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *commitStore) Transact(fn func(acceptance.Transaction) error) error {
	fault := false
	s.once.Do(func() { fault = true })
	if !fault {
		return s.base.Transact(fn)
	}
	if s.mode == "fail" {
		return errInjected
	}
	if s.mode == "before commit" {
		return s.base.Transact(func(tx acceptance.Transaction) error {
			if err := fn(tx); err != nil {
				return err
			}
			close(s.entered)
			<-s.release
			return nil
		})
	}
	err := s.base.Transact(fn)
	if err != nil {
		return err
	}
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	if s.mode == "lost reply" {
		return errInjected
	}
	return nil
}

type markedWrite struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (c *markedWrite) Write(b []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(b)
}

// Metadata-only negative scope test; the underlying TCP connection is loopback.
type foreignAddress struct{ net.Conn }

func (c foreignAddress) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1234}
}

type failHistory struct {
	base historysync.Store
	once sync.Once
}

func (s *failHistory) View(account uint32, fn func(historysync.Reader) error) error {
	fail := false
	s.once.Do(func() { fail = true })
	if fail {
		return errInjected
	}
	return s.base.View(account, fn)
}
