package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/historysync"
)

func freshConsole(t *testing.T) *console {
	t.Helper()
	c, err := openConsole(filepath.Join(t.TempDir(), "console.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.close(io.Discard); err != nil {
			t.Error(err)
		}
	})
	return c
}

func command(t *testing.T, c *console, line string) string {
	t.Helper()
	var out bytes.Buffer
	if c.command(line, &out) {
		t.Fatal("unexpected quit")
	}
	return out.String()
}

func contains(t *testing.T, got string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(got, part) {
			t.Fatalf("missing %q in output:\n%s", part, got)
		}
	}
}

func TestMessagingAndIndependentHistories(t *testing.T) {
	c := freshConsole(t)
	a, b := c.clients[0], c.clients[1]
	if a.writer <= 0 || b.writer <= 0 || a.writer == b.writer {
		t.Fatal("invalid test writer namespaces")
	}
	for _, cl := range c.clients {
		for _, addr := range []net.Addr{cl.peer.conn.LocalAddr(), cl.peer.conn.RemoteAddr()} {
			tcp := addr.(*net.TCPAddr)
			if tcp.IP.To4() == nil || !tcp.IP.Equal(net.IPv4(127, 0, 0, 1)) {
				t.Fatalf("not IPv4 loopback: %s", addr)
			}
		}
	}
	text := "  Hello Привіт Zażółć e\u0301 🙂  "
	contains(t, command(t, c, "send alice bob "+text), "SEND alice (100001) -> 100002", "ACK alice seq=1 message_id=1")
	contains(t, command(t, c, "send bob alice Hello from Bob"), "ACK bob seq=1 message_id=2")
	contains(t, command(t, c, "send alice alice Self-message test"), "ACK alice seq=2 message_id=3")
	for _, account := range []string{"alice", "bob"} {
		out := command(t, c, "sync "+account)
		contains(t, out, "message_id=1 sender=100001 recipient=100002", "seq=1 text="+fmt.Sprintf("%q", text), "message_id=2 sender=100002 recipient=100001", "Replay complete")
		want := 2
		if account == "alice" {
			want = 3
			contains(t, out, "message_id=3 sender=100001 recipient=100001")
		} else if strings.Contains(out, "Self-message") {
			t.Fatal("Alice's self-message leaked into Bob's history")
		}
		if strings.Count(out, "ITEM ") != want {
			t.Fatalf("wrong account history: %s", out)
		}
		// Each manual replay starts from zero; no cursor is kept on the client.
		if again := command(t, c, "sync "+account); again != out {
			t.Fatal("manual replay did not reproduce complete account history")
		}
	}
	contains(t, command(t, c, "retry alice"), "no uncertain SEND")
}

// Capture actual bounded SQLite reads; append after page one to check that later
// pages retain H rather than accidentally capturing a new snapshot per request.
type observedHistory struct {
	base    historysync.Store
	through []int64
	max     []int
	after   func() error
}

type observedReader struct {
	historysync.Reader
	owner *observedHistory
}

func (r observedReader) Records(after, through int64, max int) ([]historysync.Record, error) {
	r.owner.through = append(r.owner.through, through)
	r.owner.max = append(r.owner.max, max)
	return r.Reader.Records(after, through, max)
}

func (s *observedHistory) View(account uint32, fn func(historysync.Reader) error) error {
	if err := s.base.View(account, func(r historysync.Reader) error {
		return fn(observedReader{r, s})
	}); err != nil {
		return err
	}
	if s.after != nil {
		fn := s.after
		s.after = nil
		return fn()
	}
	return nil
}

func stopPeers(t *testing.T, c *console) {
	t.Helper()
	for _, cl := range c.clients {
		if err := disconnect(cl); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBoundedReplayPreservesSnapshot(t *testing.T) {
	c := freshConsole(t)
	a, b := c.clients[0], c.clients[1]
	for i := 1; i <= 18; i++ {
		if err := c.send(a, b, fmt.Sprintf("message %d", i), io.Discard); err != nil || a.pending != nil {
			t.Fatalf("send: %v", err)
		}
	}
	stopPeers(t, c)
	observer := &observedHistory{base: c.store, after: func() error {
		_, err := c.acceptor.Accept(acceptance.TrustedContext{AccountID: b.account, WriterID: b.writer}, acceptance.Request{ClientSeq: 1, Recipient: a.account, Text: "newer than H"})
		return err
	}}
	var err error
	c.history, err = historysync.New(observer)
	if err != nil {
		t.Fatal(err)
	}
	out := command(t, c, "sync alice")
	contains(t, out, "END alice next=8 through=18 items=8", "END alice next=16 through=18 items=8", "END alice next=18 through=18 items=2")
	if strings.Count(out, "ITEM ") != 18 || strings.Contains(out, "newer than H") || len(observer.through) != 3 {
		t.Fatalf("wrong snapshot replay: %s", out)
	}
	for i, h := range observer.through {
		if h != 18 || observer.max[i] != 9 { // Eight plus the store's lookahead.
			t.Fatalf("unbounded or changed read: H=%d max=%d", h, observer.max[i])
		}
	}
	contains(t, command(t, c, "sync alice"), "newer than H", "through=19")
}

// Only test adapters inject storage faults; the executable has no fault commands.
type uncertainStore struct {
	base     acceptance.Store
	commit   bool
	failures int
}

func (s *uncertainStore) Transact(fn func(acceptance.Transaction) error) error {
	if s.failures == 0 {
		return s.base.Transact(fn)
	}
	s.failures--
	if s.commit {
		if err := s.base.Transact(fn); err != nil {
			return err
		}
	}
	return errors.New("injected uncertain storage outcome")
}

func TestRetryableRetainsExactPendingRequest(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed=%v", committed), func(t *testing.T) {
			c := freshConsole(t)
			stopPeers(t, c)
			var err error
			c.acceptor, err = acceptance.New(&uncertainStore{base: c.store, commit: committed, failures: 2}, func(_ acceptance.TrustedContext, _ acceptance.Request) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			a := c.clients[0]
			text := "  Привіт 🙂  "
			out := command(t, c, "send alice bob "+text)
			contains(t, out, "RETRYABLE alice seq=1 (not ACK)")
			if a.pending == nil || a.pending.Text != text || a.pending.Recipient != 100002 || a.pending.ClientSeq != 1 {
				t.Fatalf("pending request lost: %+v", a.pending)
			}
			original := *a.pending
			contains(t, command(t, c, "send alice alice replacement"), "uncertain SEND remains")
			if a.seq != 1 || *a.pending != original {
				t.Fatal("new SEND changed uncertain key or payload")
			}
			// Bob remains independent while Alice has an unresolved request.
			contains(t, command(t, c, "sync bob"), "Replay complete")
			contains(t, command(t, c, "retry alice"), "RETRYABLE alice seq=1 (not ACK)")
			if *a.pending != original {
				t.Fatal("retry changed the payload")
			}
			contains(t, command(t, c, "retry alice"), "ACK alice seq=1 message_id=1")
			if a.pending != nil || a.seq != 1 {
				t.Fatal("ACK did not resolve pending state")
			}
			history := command(t, c, "sync bob")
			if strings.Count(history, "ITEM ") != 1 {
				t.Fatalf("retry created duplicate history: %s", history)
			}
			contains(t, history, "text="+fmt.Sprintf("%q", text))
			contains(t, command(t, c, "send alice bob next"), "ACK alice seq=2 message_id=2")
		})
	}
}

func TestDisconnectRetryAndShutdownWarning(t *testing.T) {
	c := freshConsole(t)
	a := c.clients[0]
	old := a.peer
	old.conn.Close() // Deterministic local I/O failure, no sleeps or packet races.
	contains(t, command(t, c, "send alice bob uncertain"), "UNCERTAIN alice seq=1", "Error:")
	if a.pending == nil || a.peer != nil {
		t.Fatal("failed socket was retained or pending lost")
	}
	contains(t, command(t, c, "retry alice"), "ACK alice seq=1 message_id=1")
	if a.peer == nil || a.peer == old {
		t.Fatal("retry did not reconnect")
	}
	a.peer.conn.Close()
	contains(t, command(t, c, "send alice bob warn on exit"), "UNCERTAIN alice seq=2")
	var warning bytes.Buffer
	if err := c.close(&warning); err != nil {
		t.Fatal(err)
	}
	contains(t, warning.String(), "WARNING: alice has uncertain SEND seq=2", "no durable outbox", "acceptance may already have committed")
}

func TestDefinitiveRejectionAndConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict=%v", conflict), func(t *testing.T) {
			c := freshConsole(t)
			stopPeers(t, c)
			a := c.clients[0]
			if conflict {
				if _, err := c.acceptor.Accept(acceptance.TrustedContext{AccountID: a.account, WriterID: a.writer}, acceptance.Request{ClientSeq: 1, Recipient: 100002, Text: "previously accepted"}); err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				c.acceptor, err = acceptance.New(c.store, func(_ acceptance.TrustedContext, _ acceptance.Request) bool { return false })
				if err != nil {
					t.Fatal(err)
				}
			}
			want := "REJECTED"
			if conflict {
				want = "CONFLICT"
			}
			contains(t, command(t, c, "send alice bob different"), want+" alice seq=1 (not ACK)")
			if a.pending != nil || a.seq != 1 {
				t.Fatal("definitive failure not resolved")
			}
			contains(t, command(t, c, "retry alice"), "no uncertain SEND")
			contains(t, command(t, c, "send alice bob next"), "seq=2")
		})
	}
}

func TestCommandsAndTextBoundaries(t *testing.T) {
	c := freshConsole(t)
	for _, line := range []string{"unknown", "help extra", "quit extra", "send", "send eve bob hi", "send alice eve hi", "send alice bob", "sync eve", "sync alice extra", "retry bob extra", "send alice bob " + strings.Repeat("x", 501), "send alice bob \xff"} {
		contains(t, command(t, c, line), "Error:")
	}
	if c.clients[0].seq != 0 || c.clients[1].seq != 0 {
		t.Fatal("invalid command allocated a sequence")
	}
	if got := command(t, c, "  "); got != "" {
		t.Fatal(got)
	}
	contains(t, command(t, c, "help"), "send <alice|bob>", "sync", "retry", "quit")
	maxText := strings.Repeat("🙂", 500)
	contains(t, command(t, c, "send bob alice "+maxText), "ACK bob seq=1 message_id=1")
	contains(t, command(t, c, "sync alice"), "text="+fmt.Sprintf("%q", maxText))
	c.clients[0].seq = maxSequence
	contains(t, command(t, c, "send alice bob exhausted"), "sequence exhausted")
	if !c.command("quit", io.Discard) {
		t.Fatal("quit ignored")
	}
}

func TestCloseJoinsHandlersAndClosesDatabase(t *testing.T) {
	c := freshConsole(t)
	peers := []*connection{c.clients[0].peer, c.clients[1].peer}
	if err := c.close(io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		if _, err := p.conn.Write([]byte{0}); err == nil {
			t.Fatal("socket still writable after close")
		}
		select {
		case <-p.done:
			t.Fatal("handler completion not joined")
		default:
		}
		// The ephemeral listener closed at pairing, not merely at shutdown.
		dial, err := net.DialTimeout("tcp4", p.conn.RemoteAddr().String(), time.Second)
		if err == nil {
			dial.Close()
			t.Fatal("temporary listener remains open")
		}
	}
	if err := c.store.Transact(func(acceptance.Transaction) error { return nil }); err == nil {
		t.Fatal("SQLite still open")
	}
	if err := c.connect(c.clients[0]); err == nil {
		t.Fatal("reconnected a closed console")
	}
	if err := c.close(io.Discard); err != nil {
		t.Fatal("close is not idempotent:", err)
	}
}

func TestRunValidationAndEOF(t *testing.T) {
	for _, args := range [][]string{nil, {"-unknown"}, {"-db", "x", "extra"}, {"-db", ":memory:"}, {"-db", filepath.Join(t.TempDir(), "missing", "db.sqlite")}} {
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted invalid flags/path: %v", args)
		}
	}
	if err := run([]string{"-help"}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "eof.sqlite")
	if err := run([]string{"-db", path}, strings.NewReader("send alice bob EOF test\n"), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"-db", path}, strings.NewReader("sync bob\nquit\n"), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	contains(t, out.String(), "EOF test", "Replay complete")
	if err := run([]string{"-db", path}, strings.NewReader(strings.Repeat("x", 8192)), io.Discard, io.Discard); err == nil {
		t.Fatal("unbounded console input accepted")
	}
}

// Run the real console entry function in separate OS processes, not a fake store
// or two objects sharing a database/sql pool. The parent controls stdin/EOF.
func TestConsoleProcess(t *testing.T) {
	if os.Getenv("OGM_LOCAL_CONSOLE_HELPER") != "1" {
		return
	}
	if err := run([]string{"-db", os.Args[len(os.Args)-1]}, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestPersistenceAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persistent.sqlite")
	process := func(input string) string {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestConsoleProcess$", "--", path)
		cmd.Env = append(os.Environ(), "OGM_LOCAL_CONSOLE_HELPER=1")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("console process failed: %v\n%s", err, out)
		}
		return string(out)
	}
	first := process("send alice bob persisted Alice\nsend bob alice persisted Bob\nsend alice alice private self\nquit\n")
	contains(t, first, "ACK alice seq=1 message_id=1", "ACK bob seq=1 message_id=2", "ACK alice seq=2 message_id=3")
	second := process("sync alice\nsync bob\nsend alice bob new writer\nquit\n")
	contains(t, second, "ITEM alice cursor=1 message_id=1", "ITEM alice cursor=2 message_id=2", "ITEM alice cursor=3 message_id=3", "ITEM bob cursor=1 message_id=1", "ITEM bob cursor=2 message_id=2", "ACK alice seq=1 message_id=4")
	if strings.Count(second, "ITEM ") != 5 {
		t.Fatal("incorrect persisted history:", second)
	}
	// Extract both startup writers, verifying they are fresh in the next process.
	writers := func(output string) [2]int64 {
		var ids [2]int64
		for _, line := range strings.Split(output, "\n") {
			if _, err := fmt.Sscanf(line, "alice (100001): test writer=%d", &ids[0]); err == nil {
				continue
			}
			fmt.Sscanf(line, "bob (100002): test writer=%d", &ids[1])
		}
		return ids
	}
	w1, w2 := writers(first), writers(second)
	for i := range w1 {
		if w1[i] <= 0 || w2[i] <= 0 || w1[i] == w2[i] {
			t.Fatalf("writer namespace reused across processes: %v %v", w1, w2)
		}
	}
}
