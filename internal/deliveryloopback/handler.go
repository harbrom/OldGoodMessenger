// Package deliveryloopback is a loopback-only Delivery v1 integration experiment.
// It creates no listeners and provides no identity handshake or authentication.
package deliveryloopback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
)

const (
	FrameTimeout = 5 * time.Second
	WriteTimeout = 5 * time.Second
)

var ErrConfiguration = errors.New("loopback connection, context and services are required")
var ErrProtocol = errors.New("only Delivery v1 SEND and SYNC requests are allowed")

func ipv4Loopback(address net.Addr) bool {
	tcp, ok := address.(*net.TCPAddr)
	return ok && tcp.IP.To4() != nil && tcp.IP.IsLoopback()
}

// Handle takes responsibility for closing the supplied connection on every exit.
// The caller establishes account/writer ownership OUTSIDE the byte stream, owns
// service/database lifetimes, and waits for Handle before closing those services.
// Only IPv4 loopback peers are accepted. Cancellation closes I/O; an already
// executing storage operation can still commit and must finish before return.
func Handle(ctx context.Context, conn net.Conn, trusted acceptance.TrustedContext,
	acceptor *acceptance.Acceptor, history *historysync.Service) error {
	if conn == nil {
		return ErrConfiguration
	}
	defer conn.Close()
	if ctx == nil || acceptor == nil || history == nil || !deliveryv1.ValidID(trusted.AccountID) || trusted.WriterID <= 0 ||
		!ipv4Loopback(conn.LocalAddr()) || !ipv4Loopback(conn.RemoteAddr()) {
		return ErrConfiguration
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(closed); conn.Close() })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		request, err := readRequest(conn)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Clean disconnect while waiting for a new request.
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var response []deliveryv1.Frame
		switch request.Kind {
		case deliveryv1.Send:
			result, err := acceptor.Accept(trusted, acceptance.Request{ClientSeq: request.ClientSeq, Recipient: request.Recipient, Text: request.Text})
			if err != nil {
				code := byte(deliveryv1.Retryable)
				switch {
				case errors.Is(err, acceptance.ErrInvalid), errors.Is(err, acceptance.ErrRejected):
					code = deliveryv1.Rejected
				case errors.Is(err, acceptance.ErrConflict):
					code = deliveryv1.Conflict
				}
				response = []deliveryv1.Frame{failure(request, code)}
			} else {
				// Accept returns success only after its store reports successful commit.
				response = []deliveryv1.Frame{{Kind: deliveryv1.Ack, ClientSeq: result.ClientSeq, MessageID: result.MessageID}}
			}
		case deliveryv1.Sync:
			response, err = history.Page(historysync.TrustedContext{AccountID: trusted.AccountID}, request)
			if err != nil {
				code := byte(deliveryv1.Retryable)
				if errors.Is(err, historysync.ErrCursorInvalid) {
					code = deliveryv1.CursorInvalid
				}
				response = []deliveryv1.Frame{failure(request, code)}
			}
		default:
			return ErrProtocol
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// One deadline bounds the entire response (at most eight ITEMs plus END).
		if err := conn.SetWriteDeadline(time.Now().Add(WriteTimeout)); err != nil {
			return err
		}
		for _, frame := range response {
			if err := deliveryv1.Write(conn, frame); err != nil {
				return err
			}
		}
	}
}

func failure(request deliveryv1.Frame, code byte) deliveryv1.Frame {
	reference := request.After
	if request.Kind == deliveryv1.Send {
		reference = request.ClientSeq
	}
	return deliveryv1.Frame{Kind: deliveryv1.Failure, RequestOp: request.Kind, Reference: reference, Code: code}
}

func readRequest(conn net.Conn) (deliveryv1.Frame, error) {
	// Indefinite idle, then one fixed completion budget starting at the first byte.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return deliveryv1.Frame{}, err
	}
	var first [1]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		return deliveryv1.Frame{}, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(FrameTimeout)); err != nil {
		return deliveryv1.Frame{}, err
	}
	f, err := deliveryv1.Read(io.MultiReader(bytes.NewReader(first[:]), conn))
	if err != nil {
		return deliveryv1.Frame{}, fmt.Errorf("read Delivery v1 frame: %w", err)
	}
	return f, nil
}
