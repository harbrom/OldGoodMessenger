package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"ogm.local/firstcontact/internal/wire"
)

const (
	MaxConnections   = 8
	QueueSize        = 16
	HandshakeTimeout = 5 * time.Second
	IOTimeout        = 5 * time.Second
)

type peer struct {
	conn net.Conn
	id   uint32
	out  chan wire.Frame
	done chan struct{}
	once sync.Once
}

func (p *peer) close() { p.once.Do(func() { close(p.done); p.conn.Close() }) }

// enqueue never blocks the sender and disconnects a slow recipient.
func (p *peer) enqueue(f wire.Frame) bool {
	select {
	case <-p.done:
		return false
	default:
	}
	select {
	case p.out <- f:
		return true
	case <-p.done:
		return false
	default:
		p.close()
		return false
	}
}

type Server struct {
	mu          sync.Mutex
	peers       map[uint32]*peer
	connections map[*peer]bool
	closing     bool
}

func New() *Server {
	return &Server{peers: make(map[uint32]*peer), connections: make(map[*peer]bool)}
}

// Listen accepts numeric loopback addresses only, even when explicitly configured.
func Listen(address string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("only numeric loopback addresses are permitted")
	}
	return net.Listen("tcp", address)
}

func (s *Server) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.peers)
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	stop := make(chan struct{})
	var workers sync.WaitGroup
	defer func() {
		close(stop)
		listener.Close()
		s.mu.Lock()
		s.closing = true
		for p := range s.connections {
			p.close()
		}
		s.mu.Unlock()
		workers.Wait()
	}()
	go func() {
		select {
		case <-ctx.Done():
			listener.Close()
		case <-stop:
		}
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		p := &peer{conn: conn, out: make(chan wire.Frame, QueueSize), done: make(chan struct{})}
		s.mu.Lock()
		if len(s.connections) >= MaxConnections {
			s.mu.Unlock()
			// Close immediately: an excess client cannot stall the accept loop.
			p.close()
			continue
		}
		s.connections[p] = true
		s.mu.Unlock()
		workers.Add(1)
		go func() { defer workers.Done(); s.handle(p) }()
	}
}

func (s *Server) handle(p *peer) {
	defer func() {
		p.close()
		s.mu.Lock()
		if s.peers[p.id] == p {
			delete(s.peers, p.id)
		}
		delete(s.connections, p)
		s.mu.Unlock()
	}()
	p.conn.SetReadDeadline(time.Now().Add(HandshakeTimeout))
	f, err := wire.Read(p.conn)
	if err != nil || f.Kind != wire.Hello {
		s.fail(p, wire.BadRequest)
		return
	}
	p.id = f.ID
	s.mu.Lock()
	if s.peers[p.id] != nil || s.closing {
		s.mu.Unlock()
		s.fail(p, wire.IdentityBusy)
		return
	}
	s.peers[p.id] = p
	// READY is queued before the peer becomes visible to other senders.
	p.out <- wire.Frame{Kind: wire.Ready, ID: p.id}
	s.mu.Unlock()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer p.close()
		for {
			select {
			case <-p.done:
				return
			case f := <-p.out:
				p.conn.SetWriteDeadline(time.Now().Add(IOTimeout))
				if wire.Write(p.conn, f) != nil {
					return
				}
			}
		}
	}()
	defer func() { p.close(); <-writerDone }()
	for {
		// Idle is allowed indefinitely; once a frame starts, its completion is bounded.
		p.conn.SetReadDeadline(time.Time{})
		var first [1]byte
		if _, err = io.ReadFull(p.conn, first[:]); err != nil {
			return
		}
		p.conn.SetReadDeadline(time.Now().Add(IOTimeout))
		f, err = wire.Read(io.MultiReader(bytes.NewReader(first[:]), p.conn))
		if err != nil || f.Kind != wire.Send {
			return
		}
		s.mu.Lock()
		target := s.peers[f.ID]
		s.mu.Unlock()
		if target == nil || !target.enqueue(wire.Frame{Kind: wire.Message, ID: p.id, Text: f.Text}) {
			if !p.enqueue(wire.Frame{Kind: wire.Failure, Code: wire.Unavailable}) {
				return
			}
		}
	}
}

func (s *Server) fail(p *peer, code byte) {
	p.conn.SetWriteDeadline(time.Now().Add(IOTimeout))
	wire.Write(p.conn, wire.Frame{Kind: wire.Failure, Code: code})
}
