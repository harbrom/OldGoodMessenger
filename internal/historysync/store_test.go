package historysync

import (
	"errors"
	"sync"
	"sync/atomic"

	"ogm.local/firstcontact/internal/acceptance"
)

var errRead = errors.New("injected read error")

type fixtureState struct {
	lastID   int64
	messages map[int64]acceptance.Message
	retries  map[acceptance.RetryKey]int64
	tails    map[uint32]int64
	feeds    map[uint32][]acceptance.FeedReference
}

func newState() fixtureState {
	return fixtureState{messages: make(map[int64]acceptance.Message), retries: make(map[acceptance.RetryKey]int64), tails: make(map[uint32]int64), feeds: make(map[uint32][]acceptance.FeedReference)}
}

// This small combined fixture stays test-only. The acceptance package's fake is
// unchanged. RWMutex pins each read view; acceptance publishes under its lock.
type fixtureStore struct {
	mu               sync.RWMutex
	state            fixtureState
	fail             string
	override         []Record
	tailRead         func()
	writeAttempt     chan<- struct{}
	requested, views atomic.Int32
}

func newStore() *fixtureStore { return &fixtureStore{state: newState()} }
func (s *fixtureStore) snapshot() fixtureState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := newState()
	c.lastID = s.state.lastID
	for k, v := range s.state.messages {
		c.messages[k] = v
	}
	for k, v := range s.state.retries {
		c.retries[k] = v
	}
	for k, v := range s.state.tails {
		c.tails[k] = v
	}
	for k, v := range s.state.feeds {
		c.feeds[k] = append([]acceptance.FeedReference(nil), v...)
	}
	return c
}

func (s *fixtureStore) View(account uint32, fn func(Reader) error) error {
	s.views.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fail == "open" {
		return errRead
	}
	if err := fn(fixtureReader{s, account}); err != nil {
		return err
	}
	if s.fail == "close" {
		return errRead
	}
	return nil
}

type fixtureReader struct {
	store   *fixtureStore
	account uint32
}

func (r fixtureReader) Tail() (int64, error) {
	if r.store.fail == "tail" {
		return 0, errRead
	}
	tail := r.store.state.tails[r.account]
	if r.store.tailRead != nil {
		r.store.tailRead()
	}
	return tail, nil
}
func (r fixtureReader) Records(after, through int64, max int) ([]Record, error) {
	r.store.requested.Store(int32(max))
	if r.store.fail == "records" {
		return nil, errRead
	}
	if max < 1 || max > 9 {
		return nil, errors.New("unbounded read requested")
	}
	if r.store.override != nil {
		return r.store.override, nil
	}
	rows := make([]Record, 0, max)
	for _, ref := range r.store.state.feeds[r.account] {
		if ref.Cursor <= after || ref.Cursor > through {
			continue
		}
		m, ok := r.store.state.messages[ref.MessageID]
		if !ok {
			return nil, errors.New("missing joined message")
		}
		rows = append(rows, Record{ref, m})
		if len(rows) == max {
			break
		}
	}
	return rows, nil
}

// Only the minimal existing acceptance transaction contract is implemented here.
// Stage one Record call locally, then publish all maps/feeds under the write lock.
func (s *fixtureStore) Transact(fn func(acceptance.Transaction) error) error {
	if s.writeAttempt != nil {
		s.writeAttempt <- struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := fixtureTx{store: s}
	if err := fn(&tx); err != nil {
		return err
	}
	if tx.message != nil {
		m := *tx.message
		s.state.lastID = m.ID
		s.state.messages[m.ID] = m
		s.state.retries[m.Key] = m.ID
		for _, ref := range tx.refs {
			s.state.tails[ref.AccountID] = ref.Cursor
			s.state.feeds[ref.AccountID] = append(s.state.feeds[ref.AccountID], ref)
		}
	}
	return nil
}

type fixtureTx struct {
	store   *fixtureStore
	message *acceptance.Message
	refs    []acceptance.FeedReference
}

func (tx *fixtureTx) Lookup(key acceptance.RetryKey) (acceptance.Message, bool, error) {
	id, ok := tx.store.state.retries[key]
	return tx.store.state.messages[id], ok, nil
}
func (tx *fixtureTx) LastMessageID() (int64, error) { return tx.store.state.lastID, nil }
func (tx *fixtureTx) LastCursor(account uint32) (int64, error) {
	return tx.store.state.tails[account], nil
}
func (tx *fixtureTx) Record(m acceptance.Message, refs []acceptance.FeedReference) error {
	if tx.message != nil {
		return errors.New("multiple fixture records")
	}
	if _, exists := tx.store.state.retries[m.Key]; exists || m.ID != tx.store.state.lastID+1 {
		return errors.New("fixture overwrite")
	}
	tx.message = &m
	tx.refs = append([]acceptance.FeedReference(nil), refs...)
	return nil
}
