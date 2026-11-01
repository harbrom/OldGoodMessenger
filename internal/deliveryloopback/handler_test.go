package deliveryloopback

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
)

func TestTCPSendACKAndBothAccountHistories(t *testing.T) {
	s, a, h := fresh(t)
	p := connect(t, alice, a, h, nil)
	text := "ASCII\x00\n\u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \U0001f642"
	send(t, p, message(7, bob.AccountID, text))
	wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 7, MessageID: 1})
	frames := page(t, p, syncRequest(0, 0, 8))
	if len(frames) != 2 {
		t.Fatal("wrong Alice page length")
	}
	wantFrame(t, frames[0], deliveryv1.Frame{Kind: deliveryv1.Item, Cursor: 1, MessageID: 1, Sender: alice.AccountID, Recipient: bob.AccountID, WriterID: alice.WriterID, ClientSeq: 7, Text: text})
	wantFrame(t, frames[1], deliveryv1.Frame{Kind: deliveryv1.End, Next: 1, Through: 1})
	p.client.Close()
	if err := p.wait(t); err != nil {
		t.Fatal("orderly disconnect:", err)
	}
	b := connect(t, bob, a, h, nil)
	got := page(t, b, syncRequest(0, 0, 8))
	wantFrame(t, got[0], frames[0])
	wantFrame(t, got[1], frames[1])
	b.stop(t)
	counts(t, s, alice.AccountID, 1)
	counts(t, s, bob.AccountID, 1)
}

func TestTCPOfflineHistorySurvivesDatabaseRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.sqlite")
	s := database(t, path)
	a, h := services(t, s, s)
	p := connect(t, alice, a, h, nil)
	send(t, p, message(1, bob.AccountID, "offline"))
	ack := receive(t, p)
	p.stop(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = database(t, path)
	a, h = services(t, s, s)
	b := connect(t, bob, a, h, nil)
	frames := page(t, b, syncRequest(0, 0, 1))
	if len(frames) != 2 || frames[0].MessageID != ack.MessageID || frames[0].Text != "offline" {
		t.Fatal("offline history lost after restart")
	}
	b.stop(t)
	retry := connect(t, alice, a, h, nil)
	send(t, retry, message(1, bob.AccountID, "offline"))
	wantFrame(t, receive(t, retry), ack)
	retry.stop(t)
	counts(t, s, alice.AccountID, 1)
	counts(t, s, bob.AccountID, 1)
}

func TestTCPRetriesConflictsAndSemanticErrorsKeepConnectionUsable(t *testing.T) {
	s, a, h := fresh(t)
	p := connect(t, alice, a, h, nil)
	send(t, p, message(1, 999999, "rejected"))
	wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Failure, RequestOp: deliveryv1.Send, Reference: 1, Code: deliveryv1.Rejected})
	send(t, p, message(1, bob.AccountID, "same"))
	ack := receive(t, p)
	for i := 0; i < 2; i++ {
		send(t, p, message(1, bob.AccountID, "same"))
		wantFrame(t, receive(t, p), ack)
	}
	for _, f := range []deliveryv1.Frame{message(1, bob.AccountID, "changed"), message(1, alice.AccountID, "same")} {
		send(t, p, f)
		wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Failure, RequestOp: deliveryv1.Send, Reference: 1, Code: deliveryv1.Conflict})
	}
	send(t, p, syncRequest(99, 0, 1))
	wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Failure, RequestOp: deliveryv1.Sync, Reference: 99, Code: deliveryv1.CursorInvalid})
	if frames := page(t, p, syncRequest(0, 0, 8)); len(frames) != 2 || frames[0].Text != "same" {
		t.Fatal("semantic error corrupted connection")
	}
	p.stop(t)
	counts(t, s, alice.AccountID, 1)
	counts(t, s, bob.AccountID, 1)
}

func TestTCPUncertainCommitNeverACKsAndRecoversRetry(t *testing.T) {
	for _, mode := range []string{"fail", "lost reply"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := fresh(t)
			wrapped := &commitStore{base: s, mode: mode}
			a, h := services(t, wrapped, s)
			p := connect(t, alice, a, h, nil)
			send(t, p, message(1, bob.AccountID, "uncertain"))
			wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Failure, RequestOp: deliveryv1.Send, Reference: 1, Code: deliveryv1.Retryable})
			send(t, p, message(1, bob.AccountID, "uncertain"))
			wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 1, MessageID: 1})
			p.stop(t)
			counts(t, s, alice.AccountID, 1)
			counts(t, s, bob.AccountID, 1)
		})
	}
	s, _, _ := fresh(t)
	a, h := services(t, s, &failHistory{base: s})
	p := connect(t, alice, a, h, nil)
	send(t, p, syncRequest(0, 0, 1))
	wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Failure, RequestOp: deliveryv1.Sync, Reference: 0, Code: deliveryv1.Retryable})
	frames := page(t, p, syncRequest(0, 0, 1))
	if len(frames) != 1 {
		t.Fatal("service failure left partial page")
	}
	p.stop(t)
}

func TestTCPLostACKAfterClientCloseDoesNotDuplicate(t *testing.T) {
	s, _, _ := fresh(t)
	entered, release := make(chan struct{}), make(chan struct{})
	wrapped := &commitStore{base: s, entered: entered, release: release}
	a, h := services(t, wrapped, s)
	p := connect(t, alice, a, h, nil)
	var cleanup sync.Once
	defer cleanup.Do(func() { close(release) })
	send(t, p, message(1, bob.AccountID, "lost ACK"))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("commit did not reach reply gate")
	}
	p.client.Close()
	p.cancel()
	cleanup.Do(func() { close(release) })
	p.wait(t)
	retry := connect(t, alice, a, h, nil)
	send(t, retry, message(1, bob.AccountID, "lost ACK"))
	wantFrame(t, receive(t, retry), deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 1, MessageID: 1})
	retry.stop(t)
	counts(t, s, alice.AccountID, 1)
	counts(t, s, bob.AccountID, 1)
}

func TestTCPHistoryPaginationIsolationAndSelfMessage(t *testing.T) {
	s, a, h := fresh(t)
	p := connect(t, alice, a, h, nil)
	for i := int64(1); i <= 9; i++ {
		to := bob.AccountID
		if i == 5 {
			to = alice.AccountID
		}
		send(t, p, message(i, to, "history"))
		wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: i, MessageID: i})
	}
	for _, limit := range []byte{1, 2, 8} {
		after, through := int64(0), int64(0)
		for calls := 0; ; calls++ {
			if calls > 9 {
				t.Fatal("unbounded pagination")
			}
			frames := page(t, p, syncRequest(after, through, limit))
			n := int64(len(frames) - 1)
			for i, f := range frames[:len(frames)-1] {
				if f.Cursor != after+int64(i)+1 || f.MessageID != f.Cursor {
					t.Fatal("history order changed")
				}
			}
			last := frames[len(frames)-1]
			wantFrame(t, last, deliveryv1.Frame{Kind: deliveryv1.End, Next: after + n, Through: 9})
			if last.Next == 9 {
				break
			}
			after, through = last.Next, last.Through
		}
	}
	p.stop(t)
	b := connect(t, bob, a, h, nil)
	frames := page(t, b, syncRequest(0, 0, 8))
	if len(frames) != 9 {
		t.Fatal("Bob history count includes self-message or omits incoming message")
	}
	for _, f := range frames[:8] {
		if f.MessageID == 5 {
			t.Fatal("self-message exposed to Bob")
		}
	}
	wantFrame(t, frames[8], deliveryv1.Frame{Kind: deliveryv1.End, Next: 8, Through: 8})
	b.stop(t)
	u := connect(t, nobody, a, h, nil)
	empty := page(t, u, syncRequest(0, 0, 8))
	if len(empty) != 1 {
		t.Fatal("unrelated account saw history")
	}
	wantFrame(t, empty[0], deliveryv1.Frame{Kind: deliveryv1.End})
	u.stop(t)
	counts(t, s, alice.AccountID, 9)
	counts(t, s, bob.AccountID, 8)
}

func TestTCPSplitCombinedFramesAndPartialWrites(t *testing.T) {
	s, a, h := fresh(t)
	p := connect(t, alice, a, h, func(c net.Conn) net.Conn { return &observedConn{Conn: c, maxRead: 1, maxWrite: 2} })
	b, err := message(1, bob.AccountID, "split").Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range [][]byte{b[:1], b[1:5], b[5:]} {
		if _, err := p.client.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 1, MessageID: 1})
	one, _ := message(2, bob.AccountID, "combined").Marshal()
	two, _ := syncRequest(0, 0, 2).Marshal()
	if _, err := p.client.Write(append(one, two...)); err != nil {
		t.Fatal(err)
	}
	wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 2, MessageID: 2})
	for i := int64(1); i <= 2; i++ {
		f := receive(t, p)
		if f.Kind != deliveryv1.Item || f.MessageID != i {
			t.Fatal("combined requests reordered")
		}
	}
	wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.End, Next: 2, Through: 2})
	p.stop(t)
	counts(t, s, alice.AccountID, 2)
}

func TestTCPMalformedAndUnexpectedRequestsCloseWithoutCommit(t *testing.T) {
	s, a, h := fresh(t)
	valid, _ := message(1, bob.AccountID, "A").Marshal()
	badUTF := append([]byte(nil), valid...)
	badUTF[len(badUTF)-1] = 0xff
	ack, _ := (deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 1, MessageID: 1}).Marshal()
	cases := [][]byte{{0xff, 0xff}, {0, 0}, {0, 9, 0x12, 0, 0, 0, 0, 0, 0, 0, 0}, badUTF, ack, {0, 5, 1, 0, 1, 0x86, 0xa1}}
	for _, b := range cases {
		p := connect(t, alice, a, h, nil)
		if _, err := p.client.Write(b); err != nil {
			t.Fatal(err)
		}
		if err := p.wait(t); err == nil {
			t.Fatal("malformed/unexpected frame accepted")
		}
		if frame, err := deliveryv1.Read(p.client); err == nil {
			t.Fatalf("malformed input got response %+v", frame)
		}
		p.stop(t)
	}
	p := connect(t, alice, a, h, nil)
	if _, err := p.client.Write(valid[:4]); err != nil {
		t.Fatal(err)
	}
	if err := p.client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(t); err == nil {
		t.Fatal("mid-frame EOF treated as request")
	}
	p.stop(t)
	counts(t, s, alice.AccountID, 0)
	counts(t, s, bob.AccountID, 0)
}

func TestTCPDeadlineAndIdleCancellationCleanup(t *testing.T) {
	_, a, h := fresh(t)
	reads, writes := make(chan time.Time, 8), make(chan time.Time, 8)
	p := connect(t, alice, a, h, func(c net.Conn) net.Conn { return &observedConn{Conn: c, readDeadlines: reads, writeDeadlines: writes} })
	select {
	case d := <-reads:
		if !d.IsZero() {
			t.Fatal("idle connection received a timeout")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("idle read did not start")
	}
	send(t, p, message(1, bob.AccountID, "deadline"))
	receive(t, p)
	select {
	case d := <-reads:
		if d.IsZero() {
			t.Fatal("started frame had no completion deadline")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("completion deadline missing")
	}
	select {
	case d := <-writes:
		if d.IsZero() {
			t.Fatal("write deadline missing")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write deadline missing")
	}
	p.cancel()
	if err := p.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatal("idle cancellation did not release handler", err)
	}
	for _, write := range []bool{false, true} {
		p := connect(t, alice, a, h, func(c net.Conn) net.Conn { return &observedConn{Conn: c, expireRead: !write, expireWrite: write} })
		if write {
			send(t, p, syncRequest(0, 0, 1))
		} else {
			if _, err := p.client.Write([]byte{0}); err != nil {
				t.Fatal(err)
			}
		}
		err := p.wait(t)
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatalf("stalled I/O did not terminate by deadline: %v", err)
		}
		p.stop(t)
	}
}

func TestTCPDisconnectDuringPartialResponseAndShutdown(t *testing.T) {
	s, a, h := fresh(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var cleanup sync.Once
	defer cleanup.Do(func() { close(release) })
	p := connect(t, alice, a, h, func(c net.Conn) net.Conn { return &gatedWrite{Conn: c, entered: entered, release: release} })
	send(t, p, message(1, bob.AccountID, "partial ACK"))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("response did not start")
	}
	var first [1]byte
	if _, err := io.ReadFull(p.client, first[:]); err != nil {
		t.Fatal(err)
	}
	p.client.Close()
	p.cancel()
	cleanup.Do(func() { close(release) })
	p.wait(t)
	counts(t, s, alice.AccountID, 1)
	counts(t, s, bob.AccountID, 1)
	retry := connect(t, alice, a, h, nil)
	send(t, retry, message(1, bob.AccountID, "partial ACK"))
	wantFrame(t, receive(t, retry), deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 1, MessageID: 1})
	retry.stop(t)
}

func TestTCPNoResponseBeforeSQLiteCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commit-gate.sqlite")
	s := database(t, path)
	reader := database(t, path)
	entered, release, written := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var cleanup sync.Once
	defer cleanup.Do(func() { close(release) })
	a, h := services(t, &commitStore{base: s, mode: "before commit", entered: entered, release: release}, s)
	p := connect(t, alice, a, h, func(c net.Conn) net.Conn { return &markedWrite{Conn: c, started: written} })
	send(t, p, message(1, bob.AccountID, "not yet committed"))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("transaction did not reach commit gate")
	}
	select {
	case <-written:
		t.Fatal("response started before successful commit")
	default:
	}
	if err := reader.View(alice.AccountID, func(r historysync.Reader) error {
		tail, err := r.Tail()
		if err != nil {
			return err
		}
		if tail != 0 {
			return errors.New("uncommitted message visible")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cleanup.Do(func() { close(release) })
	wantFrame(t, receive(t, p), deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 1, MessageID: 1})
	p.stop(t)
	counts(t, s, alice.AccountID, 1)
}

func TestTCPRejectsInvalidInjectedContextAndNonLoopbackMetadata(t *testing.T) {
	s, a, h := fresh(t)
	for _, who := range []acceptance.TrustedContext{{AccountID: 0, WriterID: 1}, {AccountID: alice.AccountID, WriterID: 0}} {
		p := connect(t, who, a, h, nil)
		if err := p.wait(t); err != ErrConfiguration {
			t.Fatal("invalid context accepted:", err)
		}
		p.stop(t)
	}
	p := connect(t, alice, a, h, func(c net.Conn) net.Conn { return foreignAddress{c} })
	if err := p.wait(t); err != ErrConfiguration {
		t.Fatal("nonloopback metadata accepted:", err)
	}
	p.stop(t)
	counts(t, s, alice.AccountID, 0)
}
