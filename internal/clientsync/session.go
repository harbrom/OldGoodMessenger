// Package clientsync is an isolated, sequentially driven Delivery v1 reference
// state machine. It performs no networking, authentication or durable storage.
package clientsync

import (
	"errors"

	"ogm.local/firstcontact/internal/deliveryv1"
)

var (
	ErrInvalid     = errors.New("invalid client synchronization configuration or replay position")
	ErrOutstanding = errors.New("a SYNC request is already outstanding")
	ErrRecovery    = errors.New("synchronization requires reconnect/recovery")
	ErrProtocol    = errors.New("invalid frame or synchronization transition")
	ErrStoreState  = errors.New("invalid or regressing local checkpoint")
)

// Store is bound by the caller to one server history, account and device.
// Upsert acknowledges a complete canonical ITEM record, keyed by message ID;
// identical replay is a no-op and conflicting immutable fields must fail.
// AdvanceCheckpoint atomically stores max(current, candidate) and returns the
// actual stored value. It must not overtake earlier acknowledged Upsert writes.
// Write errors may have uncertain outcomes; recovery reads actual stored state.
// Durability is the implementation's separate responsibility, not implied here.
type Store interface {
	Checkpoint() (int64, error)
	Upsert(deliveryv1.Frame) error
	AdvanceCheckpoint(candidate int64) (int64, error)
}

type page struct {
	request            deliveryv1.Frame
	count              int
	lastCursor, lastID int64
}

// Session methods must be called serially. The account is caller-established,
// never taken from a received frame; numeric validation is not authentication.
type Session struct {
	account                          uint32
	limit                            byte
	store                            Store
	checkpoint, pendingTail          int64
	active                           *page
	after, through                   int64
	continuation, mustSync, recovery bool
}

func New(account uint32, limit byte, store Store) (*Session, error) {
	if !deliveryv1.ValidID(account) || limit < 1 || limit > 8 || store == nil {
		return nil, ErrInvalid
	}
	s := &Session{account: account, limit: limit, store: store}
	if err := s.Reconnect(); err != nil {
		return nil, err
	}
	return s, nil
}

// Disconnect abandons only request state. Stored items/checkpoints remain.
// The caller must discard the old stream and buffered frames before Reconnect.
func (s *Session) Disconnect() {
	s.active = nil
	s.continuation = false
	s.recovery = true
	s.after, s.through, s.pendingTail = 0, 0, 0
}

// Reconnect also recovers after storage/protocol failure. Always start a fresh
// snapshot from the actual stored checkpoint, even without a CHANGED hint.
func (s *Session) Reconnect() error {
	s.Disconnect()
	checkpoint, err := s.store.Checkpoint()
	if err != nil {
		return err
	}
	if checkpoint < 0 || checkpoint < s.checkpoint {
		return ErrStoreState
	}
	s.checkpoint = checkpoint
	s.recovery = false
	s.mustSync = true
	return nil
}

// Checkpoint returns the last confirmed committed value. After a failure it is
// unavailable until recovery, since a write may have succeeded without a reply.
func (s *Session) Checkpoint() (int64, error) {
	if s.recovery {
		return 0, ErrRecovery
	}
	return s.checkpoint, nil
}

// NeedsSync indicates initial/reconnect work, another page, or a newer hint.
// Next can also be called manually when idle to capture a fresh snapshot.
func (s *Session) NeedsSync() bool {
	return !s.recovery && s.active == nil && (s.mustSync || s.continuation || s.pendingTail > s.checkpoint)
}

func (s *Session) available() error {
	if s.recovery {
		return ErrRecovery
	}
	if s.active != nil {
		return ErrOutstanding
	}
	return nil
}
func (s *Session) start(after, through int64) deliveryv1.Frame {
	req := deliveryv1.Frame{Kind: deliveryv1.Sync, After: after, Through: through, Limit: s.limit}
	s.active = &page{request: req, lastCursor: after}
	s.continuation = false
	s.mustSync = false
	return req
}

func (s *Session) Next() (deliveryv1.Frame, error) {
	if err := s.available(); err != nil {
		return deliveryv1.Frame{}, err
	}
	if s.continuation {
		return s.start(s.after, s.through), nil
	}
	return s.start(s.checkpoint, 0), nil
}

// Replay starts an older retrieval position, replacing an unissued continuation.
// Its later pages use END.next, independently of a newer stored checkpoint.
func (s *Session) Replay(after int64) (deliveryv1.Frame, error) {
	if err := s.available(); err != nil {
		return deliveryv1.Frame{}, err
	}
	if after < 0 || after > s.checkpoint {
		return deliveryv1.Frame{}, ErrInvalid
	}
	return s.start(after, 0), nil
}

func (s *Session) fail(err error) error { s.Disconnect(); return err }

// Handle consumes one already-decoded frame. Existing bounded codec validation
// is reused via Marshal; its bytes are discarded and never sent anywhere.
func (s *Session) Handle(f deliveryv1.Frame) error {
	if s.recovery {
		return ErrRecovery
	}
	if _, err := f.Marshal(); err != nil {
		return s.fail(ErrProtocol)
	}
	switch f.Kind {
	case deliveryv1.Changed:
		if f.Tail > s.pendingTail {
			s.pendingTail = f.Tail
		}
		return nil
	case deliveryv1.Ack:
		// Outbox handling belongs to the caller. Only an active page admits this
		// interleave; the synchronization-only consumer cannot correlate idle ACKs.
		if s.active == nil {
			return s.fail(ErrProtocol)
		}
		return nil
	case deliveryv1.Item:
		p := s.active
		if p == nil || p.count >= int(p.request.Limit) || (f.Sender != s.account && f.Recipient != s.account) ||
			f.Cursor <= p.lastCursor || f.MessageID <= p.lastID || (p.request.Through != 0 && f.Cursor > p.request.Through) {
			return s.fail(ErrProtocol)
		}
		// Store only fields that exist in ITEM's wire layout.
		item := deliveryv1.Frame{Kind: deliveryv1.Item, Cursor: f.Cursor, MessageID: f.MessageID,
			Sender: f.Sender, Recipient: f.Recipient, WriterID: f.WriterID, ClientSeq: f.ClientSeq, Text: f.Text}
		if err := s.store.Upsert(item); err != nil {
			return s.fail(err)
		}
		p.count++
		p.lastCursor, p.lastID = f.Cursor, f.MessageID
		return nil
	case deliveryv1.End:
		p := s.active
		if p == nil || (p.request.Through != 0 && f.Through != p.request.Through) || f.Through < p.lastCursor ||
			f.Next < p.request.After || f.Next > f.Through {
			return s.fail(ErrProtocol)
		}
		more := f.Next < f.Through
		if more && (p.count != int(p.request.Limit) || f.Next != p.lastCursor) {
			return s.fail(ErrProtocol)
		}
		actual, err := s.store.AdvanceCheckpoint(f.Next)
		if err != nil {
			return s.fail(err)
		}
		if actual < f.Next || actual < s.checkpoint {
			return s.fail(ErrStoreState)
		}
		s.checkpoint = actual
		s.active = nil
		s.continuation, s.after, s.through = more, f.Next, f.Through
		if s.pendingTail <= actual {
			s.pendingTail = 0
		}
		return nil
	default:
		return s.fail(ErrProtocol)
	}
}
