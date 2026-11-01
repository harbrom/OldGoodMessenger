package relay

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"ogm.local/firstcontact/internal/testclient"
	"ogm.local/firstcontact/internal/wire"
)

func start(t *testing.T) (*Server, string) {
	t.Helper()
	l, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(7 * time.Second):
			t.Error("server shutdown hung")
		}
	})
	return s, l.Addr().String()
}

func dial(t *testing.T, addr string, id uint32) *testclient.Client {
	t.Helper()
	c, err := testclient.Dial(addr, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func receive(t *testing.T, c *testclient.Client) wire.Frame {
	t.Helper()
	c.Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	f, err := c.Receive()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func wait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBidirectionalAndSelfMessaging(t *testing.T) {
	s, addr := start(t)
	a := dial(t, addr, wire.FirstID)
	b := dial(t, addr, wire.SecondID)
	if s.Active() != 2 {
		t.Fatal("expected two active identities")
	}
	for i := 0; i < 10; i++ {
		text := fmt.Sprintf("%d ASCII / \u041f\u0440\u0438\u0432\u0456\u0442 / Za\u017c\u00f3\u0142\u0107 / \U0001f642", i)
		errors := make(chan error, 2)
		go func() { errors <- a.Send(wire.SecondID, text) }()
		go func() { errors <- b.Send(wire.FirstID, text) }()
		for j := 0; j < 2; j++ {
			if err := <-errors; err != nil {
				t.Fatal(err)
			}
		}
		for _, pair := range []struct {
			c    *testclient.Client
			from uint32
		}{{a, wire.SecondID}, {b, wire.FirstID}} {
			got := receive(t, pair.c)
			if got.Kind != wire.Message || got.ID != pair.from || got.Text != text {
				t.Fatalf("wrong message: %+v", got)
			}
		}
	}
	if err := a.Send(wire.FirstID, "self"); err != nil {
		t.Fatal(err)
	}
	if f := receive(t, a); f.Text != "self" || f.ID != wire.FirstID {
		t.Fatal(f)
	}
}

func TestReconnectDuplicateAndOffline(t *testing.T) {
	s, addr := start(t)
	b := dial(t, addr, wire.SecondID)
	for i := 0; i < 10; i++ {
		a := dial(t, addr, wire.FirstID)
		if duplicate, err := testclient.Dial(addr, wire.FirstID); err == nil {
			duplicate.Close()
			t.Fatal("duplicate accepted")
		}
		if err := b.Send(wire.FirstID, "after reconnect"); err != nil {
			t.Fatal(err)
		}
		receive(t, a)
		a.Close()
		wait(t, func() bool { return s.Active() == 1 })
		if err := b.Send(wire.FirstID, "offline"); err != nil {
			t.Fatal(err)
		}
		if f := receive(t, b); f.Kind != wire.Failure || f.Code != wire.Unavailable {
			t.Fatal(f)
		}
	}
}

func TestNetworkSplitCombinedAndMalformed(t *testing.T) {
	s, addr := start(t)
	b := dial(t, addr, wire.SecondID)
	a, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.SetDeadline(time.Now().Add(2 * time.Second))
	hello, _ := (wire.Frame{Kind: wire.Hello, ID: wire.FirstID}).Marshal()
	for _, v := range hello {
		if _, err = a.Write([]byte{v}); err != nil {
			t.Fatal(err)
		}
	}
	if f, err := wire.Read(a); err != nil || f.Kind != wire.Ready {
		t.Fatalf("handshake: %v", err)
	}
	var combined []byte
	for _, text := range []string{"first", "second"} {
		data, _ := (wire.Frame{Kind: wire.Send, ID: wire.SecondID, Text: text}).Marshal()
		combined = append(combined, data...)
	}
	if _, err = a.Write(combined); err != nil {
		t.Fatal(err)
	}
	if receive(t, b).Text != "first" || receive(t, b).Text != "second" {
		t.Fatal("frame ordering")
	}
	a.Write([]byte{0xff, 0xff})
	var one [1]byte
	if _, err = a.Read(one[:]); err == nil {
		t.Fatal("malformed sender not closed")
	}
	wait(t, func() bool { return s.Active() == 1 })
	a2 := dial(t, addr, wire.FirstID)
	a2.Send(wire.SecondID, "still alive")
	if receive(t, b).Text != "still alive" {
		t.Fatal("relay failed after malformed input")
	}
}

func TestQueueBoundAndSlowRecipient(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p := &peer{conn: a, out: make(chan wire.Frame, QueueSize), done: make(chan struct{})}
	for i := 0; i < QueueSize; i++ {
		if !p.enqueue(wire.Frame{Kind: wire.Message, ID: wire.FirstID, Text: "bounded"}) {
			t.Fatal("queue closed early")
		}
	}
	if p.enqueue(wire.Frame{Kind: wire.Message, ID: wire.FirstID, Text: "overflow"}) {
		t.Fatal("overflow accepted")
	}
	select {
	case <-p.done:
	default:
		t.Fatal("slow recipient not disconnected")
	}
	if len(p.out) != QueueSize {
		t.Fatal("queue exceeded bound")
	}
}

func TestAdmissionCapAndShutdown(t *testing.T) {
	s, addr := start(t)
	for i := 0; i < MaxConnections; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
	}
	wait(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.connections) == MaxConnections })
	extra, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err = extra.Read(b[:]); err == nil {
		t.Fatal("excess connection survived")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("excess connection was not promptly closed")
	}
}

func TestInvalidListenConfiguration(t *testing.T) {
	for _, check := range []struct{ address, reason string }{
		{"", "invalid listen address"}, {"127.0.0.1", "invalid listen address"},
		{":19090", "numeric local IP"}, {"localhost:19090", "numeric local IP"},
		{"999.1.1.1:19090", "numeric local IP"},
		{"0.0.0.0:19090", "wildcard"}, {"[::]:19090", "wildcard"},
		{"[::ffff:0.0.0.0]:19090", "wildcard"}, {"224.0.0.1:19090", "multicast"},
		{"127.0.0.1:", "invalid listen port"}, {"127.0.0.1:-1", "invalid listen port"},
		{"127.0.0.1:+1", "invalid listen port"}, {"127.0.0.1:65536", "invalid listen port"},
		{"127.0.0.1:http", "invalid listen port"}, {"127.0.0.1: 80", "invalid listen port"},
	} {
		if l, err := Listen(check.address); err == nil {
			l.Close()
			t.Errorf("invalid bind accepted: %q", check.address)
		} else if !strings.Contains(err.Error(), check.reason) {
			t.Errorf("%q: expected %q, got %v", check.address, check.reason, err)
		}
	}
}

func TestExplicitListenAddress(t *testing.T) {
	// These are parse checks only: never expose a listener on a LAN in the tests.
	for _, address := range []string{DefaultListenAddress, "192.168.1.2:19090", "10.0.0.2:65535", "[::1]:0", "[fe80::1%test]:19090"} {
		if got, err := parseListenAddress(address); err != nil || got != address {
			t.Errorf("%q: got %q, %v", address, got, err)
		}
	}
	l, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	address := l.Addr().(*net.TCPAddr)
	if !address.IP.Equal(net.ParseIP("127.0.0.1")) || address.Port == 0 {
		t.Fatalf("unexpected bound address: %s", address)
	}
	// A second listener on the same address must report a bind failure clearly.
	if duplicate, err := Listen(address.String()); err == nil {
		duplicate.Close()
		t.Fatal("occupied port accepted")
	} else if !strings.Contains(err.Error(), "cannot listen on") {
		t.Fatal(err)
	}
}

func TestAbruptDisconnectAndBoundaries(t *testing.T) {
	s, addr := start(t)
	a := dial(t, addr, wire.FirstID)
	b := dial(t, addr, wire.SecondID)
	for _, text := range []string{strings.Repeat("a", 500), strings.Repeat("\U0001f642", 500)} {
		if err := a.Send(wire.SecondID, text); err != nil {
			t.Fatal(err)
		}
		if receive(t, b).Text != text {
			t.Fatal("maximum message corrupted")
		}
	}
	// TCP reset, rather than a graceful EOF, must release the identity too.
	a.Conn.(*net.TCPConn).SetLinger(0)
	a.Close()
	wait(t, func() bool { return s.Active() == 1 })
	a = dial(t, addr, wire.FirstID)
	if err := b.Send(wire.FirstID, "after reset"); err != nil {
		t.Fatal(err)
	}
	if receive(t, a).Text != "after reset" {
		t.Fatal("reset left stale state")
	}
	invalid, _ := (wire.Frame{Kind: wire.Send, ID: wire.SecondID, Text: "x"}).Marshal()
	invalid[7] = 0xff
	a.Conn.Write(invalid)
	wait(t, func() bool { return s.Active() == 1 })
	if err := b.Send(wire.SecondID, "survivor"); err != nil {
		t.Fatal(err)
	}
	if receive(t, b).Text != "survivor" {
		t.Fatal("malformed UTF-8 affected other peer")
	}
}

func TestStalledFramesExpire(t *testing.T) {
	for _, handshake := range []bool{true, false} {
		t.Run(fmt.Sprint("handshake=", handshake), func(t *testing.T) {
			t.Parallel()
			s, addr := start(t)
			c, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if !handshake {
				if err := wire.Write(c, wire.Frame{Kind: wire.Hello, ID: wire.FirstID}); err != nil {
					t.Fatal(err)
				}
				if _, err := wire.Read(c); err != nil {
					t.Fatal(err)
				}
			}
			c.SetReadDeadline(time.Now().Add(7 * time.Second))
			c.Write([]byte{0})
			if handshake {
				f, err := wire.Read(c)
				if err != nil || f.Kind != wire.Failure {
					t.Fatalf("handshake timeout: %+v %v", f, err)
				}
			}
			var one [1]byte
			if _, err = c.Read(one[:]); err == nil {
				t.Fatal("stalled connection survived")
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("server did not close stalled frame")
			}
			wait(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.connections) == 0 })
		})
	}
}
