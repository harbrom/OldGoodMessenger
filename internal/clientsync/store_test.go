package clientsync

import (
	"errors"
	"sync"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
)

var errInjected = errors.New("injected local storage failure")
var errRecordConflict = errors.New("conflicting immutable local message")

// Retained only across reference-instance recreation in tests, not process exit.
type memoryStore struct {
	mu                                 sync.Mutex
	checkpoint                         int64
	records                            map[int64]deliveryv1.Frame
	failRead, failItem                 bool
	failCheckpoint                     bool
	loseItemReply, loseCheckpointReply bool
	commits                            int
}

func newStore() *memoryStore { return &memoryStore{records: make(map[int64]deliveryv1.Frame)} }
func (s *memoryStore) Checkpoint() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failRead {
		s.failRead = false
		return 0, errInjected
	}
	return s.checkpoint, nil
}
func (s *memoryStore) Upsert(f deliveryv1.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failItem {
		s.failItem = false
		return errInjected
	}
	if old, ok := s.records[f.MessageID]; ok && old != f {
		return errRecordConflict
	}
	for id, old := range s.records {
		if old.Cursor == f.Cursor && id != f.MessageID {
			return errRecordConflict
		}
	}
	s.records[f.MessageID] = f
	if s.loseItemReply {
		s.loseItemReply = false
		return errInjected
	}
	return nil
}
func (s *memoryStore) AdvanceCheckpoint(candidate int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits++
	if s.failCheckpoint {
		s.failCheckpoint = false
		return 0, errInjected
	}
	if candidate > s.checkpoint {
		s.checkpoint = candidate
	}
	if s.loseCheckpointReply {
		s.loseCheckpointReply = false
		return 0, errInjected
	}
	return s.checkpoint, nil
}
func (s *memoryStore) snapshot() (int64, map[int64]deliveryv1.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make(map[int64]deliveryv1.Frame)
	for id, f := range s.records {
		records[id] = f
	}
	return s.checkpoint, records
}

// Test-only adapter to the existing server service, without sockets or a new
// acceptance fake. Records are immutable value fixtures, scoped by account.
type serverStore struct {
	feeds map[uint32][]historysync.Record
	tails map[uint32]int64
}

func newServerStore() *serverStore {
	return &serverStore{feeds: make(map[uint32][]historysync.Record), tails: make(map[uint32]int64)}
}
func (s *serverStore) add(account uint32, f deliveryv1.Frame) {
	r := historysync.Record{Reference: acceptance.FeedReference{AccountID: account, Cursor: f.Cursor, MessageID: f.MessageID},
		Message: acceptance.Message{ID: f.MessageID, Key: acceptance.RetryKey{AccountID: f.Sender, WriterID: f.WriterID, ClientSeq: f.ClientSeq}, Recipient: f.Recipient, Text: f.Text}}
	s.feeds[account] = append(s.feeds[account], r)
	s.tails[account] = f.Cursor
}
func (s *serverStore) View(account uint32, fn func(historysync.Reader) error) error {
	return fn(serverView{s.feeds[account], s.tails[account]})
}

type serverView struct {
	records []historysync.Record
	tail    int64
}

func (v serverView) Tail() (int64, error) { return v.tail, nil }
func (v serverView) Records(after, through int64, max int) ([]historysync.Record, error) {
	if max < 1 || max > 9 {
		return nil, errors.New("unbounded server fixture read")
	}
	rows := make([]historysync.Record, 0, max)
	for _, r := range v.records {
		if r.Reference.Cursor > after && r.Reference.Cursor <= through {
			rows = append(rows, r)
			if len(rows) == max {
				break
			}
		}
	}
	return rows, nil
}
