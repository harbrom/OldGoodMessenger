// Package acceptance implements isolated message acceptance semantics.
// A successful result describes a store commit, not a durable protocol ACK.
package acceptance

import (
	"errors"

	"ogm.local/firstcontact/internal/deliveryv1"
)

var (
	ErrInvalid    = errors.New("invalid acceptance request or trusted context")
	ErrRejected   = errors.New("message rejected")
	ErrConflict   = errors.New("retry key has different content")
	ErrExhausted  = errors.New("message ID or feed cursor exhausted")
	ErrStoreState = errors.New("invalid store allocation state")
)

// TrustedContext is supplied by a trusted caller, never decoded from SEND.
// The caller establishes account ownership and the account/writer binding.
type TrustedContext struct {
	AccountID uint32
	WriterID  int64
}

type Request struct {
	ClientSeq int64
	Recipient uint32
	Text      string
}

type RetryKey struct {
	AccountID           uint32
	WriterID, ClientSeq int64
}

// Message is an immutable value after commit; its text retains exact UTF-8 bytes.
type Message struct {
	ID        int64
	Key       RetryKey
	Recipient uint32
	Text      string
}

type FeedReference struct {
	AccountID         uint32
	Cursor, MessageID int64
}

type Result struct{ ClientSeq, MessageID int64 }

// Policy decides existence and acceptance of a NEW submission. False always
// becomes ErrRejected, without revealing blocking or suspension details.
// It must be side-effect-free and must not reenter the store transaction.
// Offline status alone must not cause rejection.
type Policy func(TrustedContext, Request) bool

// Store must serialize transactions sharing its message/key/feed allocations.
// Call fn exactly once with private staged state. On callback failure, roll back
// everything, including allocations. Publish all changes atomically at commit.
// A commit error may be uncertain: changes can exist despite a lost reply.
// Implementations must document durability separately. Do not retain fn or tx.
type Store interface {
	Transact(fn func(tx Transaction) error) error
}

// Transaction is scoped to one Store.Transact call. Lookup returns a value copy.
// Record stages the message, its retry association (including original recipient
// and bytes), feed references, and their allocation high-water marks together.
// It must not overwrite accepted messages, retry keys, or account/message refs.
type Transaction interface {
	Lookup(RetryKey) (Message, bool, error)
	LastMessageID() (int64, error)
	LastCursor(accountID uint32) (int64, error)
	Record(Message, []FeedReference) error
}

type Acceptor struct {
	store  Store
	policy Policy
}

func New(store Store, policy Policy) (*Acceptor, error) {
	if store == nil || policy == nil {
		return nil, ErrInvalid
	}
	return &Acceptor{store: store, policy: policy}, nil
}

func next(last int64) (int64, error) {
	if last < 0 {
		return 0, ErrStoreState
	}
	if last == 1<<63-1 {
		return 0, ErrExhausted
	}
	return last + 1, nil
}

func nextCursor(tx Transaction, account uint32) (int64, error) {
	last, err := tx.LastCursor(account)
	if err != nil {
		return 0, err
	}
	return next(last)
}

// Accept returns a nonzero result only after Transact reports successful commit.
// On an uncertain store error, retry the same trusted context, sequence and
// payload. No notification, transport ACK, or authentication is performed here.
func (a *Acceptor) Accept(trusted TrustedContext, req Request) (Result, error) {
	if !deliveryv1.ValidID(trusted.AccountID) || trusted.WriterID <= 0 || req.ClientSeq <= 0 ||
		!deliveryv1.ValidID(req.Recipient) || !deliveryv1.ValidText(req.Text) {
		return Result{}, ErrInvalid
	}
	key := RetryKey{trusted.AccountID, trusted.WriterID, req.ClientSeq}
	var result Result
	err := a.store.Transact(func(tx Transaction) error {
		original, found, err := tx.Lookup(key)
		if err != nil {
			return err
		}
		if found {
			if original.Recipient != req.Recipient || original.Text != req.Text {
				return ErrConflict
			}
			result = Result{req.ClientSeq, original.ID}
			return nil
		}
		if !a.policy(trusted, req) {
			return ErrRejected
		}
		last, err := tx.LastMessageID()
		if err != nil {
			return err
		}
		id, err := next(last)
		if err != nil {
			return err
		}
		senderCursor, err := nextCursor(tx, trusted.AccountID)
		if err != nil {
			return err
		}
		refs := []FeedReference{{trusted.AccountID, senderCursor, id}}
		if req.Recipient != trusted.AccountID {
			recipientCursor, err := nextCursor(tx, req.Recipient)
			if err != nil {
				return err
			}
			refs = append(refs, FeedReference{req.Recipient, recipientCursor, id})
		}
		if err := tx.Record(Message{id, key, req.Recipient, req.Text}, refs); err != nil {
			return err
		}
		result = Result{req.ClientSeq, id}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
