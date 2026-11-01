// Package historysync implements isolated, read-only Delivery v1 history pages.
// It does not manage authentication, device checkpoints, sockets or durability.
package historysync

import (
	"errors"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
)

var (
	ErrInvalid       = errors.New("invalid SYNC request or trusted account")
	ErrCursorInvalid = errors.New("invalid or future history cursor bounds")
	ErrStoreState    = errors.New("inconsistent committed history")
)

// TrustedContext is supplied separately by a trusted caller. A numeric account
// is not authentication; the caller must establish ownership before calling Page.
type TrustedContext struct{ AccountID uint32 }

// Record joins an account's feed reference with its immutable accepted message.
type Record struct {
	Reference acceptance.FeedReference
	Message   acceptance.Message
}

// Store calls fn exactly once with one consistent committed read view restricted
// to accountID, including message contents. The view cannot escape the callback.
// Concurrent acceptance must not change that view. No writes/checkpoints occur.
type Store interface {
	View(accountID uint32, fn func(Reader) error) error
}

type Reader interface {
	Tail() (int64, error)
	// Records returns the earliest min(max, remaining) records in (after, through],
	// ascending cursor, joined to their messages. It must not load the full feed:
	// at most max records may be materialized (max <= 9, including lookahead).
	Records(after, through int64, max int) ([]Record, error)
}

type Service struct{ store Store }

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	return &Service{store}, nil
}

// Page returns at most eight ITEM frames followed by one END. Any error returns
// nil, including a read-view close error after records have been constructed.
// req must be an already-decoded SYNC; unused Frame fields remain irrelevant.
func (s *Service) Page(trusted TrustedContext, req deliveryv1.Frame) ([]deliveryv1.Frame, error) {
	if !deliveryv1.ValidID(trusted.AccountID) || req.Kind != deliveryv1.Sync || req.Limit < 1 || req.Limit > 8 {
		return nil, ErrInvalid
	}
	if req.After < 0 || req.Through < 0 || (req.Through != 0 && req.After > req.Through) {
		return nil, ErrCursorInvalid
	}
	var frames []deliveryv1.Frame
	err := s.store.View(trusted.AccountID, func(reader Reader) error {
		tail, err := reader.Tail()
		if err != nil {
			return err
		}
		if tail < 0 {
			return ErrStoreState
		}
		h := req.Through
		if h == 0 {
			h = tail
		}
		if req.After > h || h > tail {
			return ErrCursorInvalid
		}
		limit := int(req.Limit)
		var records []Record
		if req.After < h {
			records, err = reader.Records(req.After, h, limit+1)
			if err != nil {
				return err
			}
		}
		if len(records) > limit+1 {
			return ErrStoreState
		}
		lastCursor, lastID := req.After, int64(0)
		for _, record := range records {
			ref, m := record.Reference, record.Message
			if ref.AccountID != trusted.AccountID || ref.Cursor <= lastCursor || ref.Cursor > h ||
				ref.MessageID != m.ID || m.ID <= lastID || !deliveryv1.ValidID(m.Key.AccountID) ||
				!deliveryv1.ValidID(m.Recipient) || m.Key.WriterID <= 0 || m.Key.ClientSeq <= 0 ||
				(m.Key.AccountID != trusted.AccountID && m.Recipient != trusted.AccountID) || !deliveryv1.ValidText(m.Text) {
				return ErrStoreState
			}
			lastCursor, lastID = ref.Cursor, m.ID
		}
		next := h
		if len(records) > limit {
			records = records[:limit]
			next = records[limit-1].Reference.Cursor
		}
		frames = make([]deliveryv1.Frame, 0, len(records)+1)
		for _, record := range records {
			ref, m := record.Reference, record.Message
			frames = append(frames, deliveryv1.Frame{
				Kind: deliveryv1.Item, Cursor: ref.Cursor, MessageID: m.ID,
				Sender: m.Key.AccountID, Recipient: m.Recipient, WriterID: m.Key.WriterID,
				ClientSeq: m.Key.ClientSeq, Text: m.Text,
			})
		}
		frames = append(frames, deliveryv1.Frame{Kind: deliveryv1.End, Next: next, Through: h})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return frames, nil
}
