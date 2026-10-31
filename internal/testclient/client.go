package testclient

import (
	"fmt"
	"net"
	"sync"
	"time"

	"ogm.local/firstcontact/internal/wire"
)

type Client struct {
	Conn net.Conn
	mu   sync.Mutex
}

func Dial(address string, id uint32) (*Client, error) {
	if !wire.ValidID(id) {
		return nil, wire.ErrInvalid
	}
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err = wire.Write(conn, wire.Frame{Kind: wire.Hello, ID: id}); err != nil {
		conn.Close()
		return nil, err
	}
	f, err := wire.Read(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if f.Kind != wire.Ready || f.ID != id {
		conn.Close()
		return nil, fmt.Errorf("identity rejected: frame=%d code=%d", f.Kind, f.Code)
	}
	conn.SetDeadline(time.Time{})
	return &Client{Conn: conn}, nil
}

func (c *Client) Send(id uint32, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return wire.Write(c.Conn, wire.Frame{Kind: wire.Send, ID: id, Text: text})
}

// Receive has one caller; closing the connection cancels it.
func (c *Client) Receive() (wire.Frame, error) { return wire.Read(c.Conn) }
func (c *Client) Close() error                 { return c.Conn.Close() }
