package acceptance

import (
	"errors"
	"sync"
)

var errInjected = errors.New("injected transaction failure")
var errUncertain = errors.New("commit reply lost; retry the same submission")

type fault int

const (
	noFault fault = iota
	lookupFailure
	idReadFailure
	cursorReadFailure
	partialRecordFailure
	partialFeedFailure
	commitFailure
	lostCommitReply
)

// Test-only, volatile fake. Cloning a small fixture state is intentional; this
// is neither a database design nor a production memory/durability strategy.
type memoryState struct {
	lastID   int64
	messages []Message
	retries  map[RetryKey]Message
	tails    map[uint32]int64
	feeds    map[uint32][]FeedReference
}

func emptyState() memoryState {
	return memoryState{retries: make(map[RetryKey]Message), tails: make(map[uint32]int64), feeds: make(map[uint32][]FeedReference)}
}

func (s memoryState) clone() memoryState {
	c := emptyState()
	c.lastID = s.lastID
	c.messages = append(c.messages, s.messages...)
	for k, v := range s.retries {
		c.retries[k] = v
	}
	for k, v := range s.tails {
		c.tails[k] = v
	}
	for k, v := range s.feeds {
		c.feeds[k] = append([]FeedReference(nil), v...)
	}
	return c
}

type memoryStore struct {
	mu       sync.Mutex
	state    memoryState
	failNext fault
	// Optional immutable test channel signals a transaction attempt before locking.
	attempted chan<- struct{}
	// The hook sees a copy of committed state while the transaction is staged.
	beforeCommit func(memoryState)
}

func newMemoryStore() *memoryStore { return &memoryStore{state: emptyState()} }
func (s *memoryStore) snapshot() memoryState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.clone()
}
func (s *memoryStore) inject(f fault) { s.mu.Lock(); defer s.mu.Unlock(); s.failNext = f }

func (s *memoryStore) Transact(fn func(Transaction) error) error {
	if s.attempted != nil {
		s.attempted <- struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failNext
	s.failNext = noFault
	staged := s.state.clone()
	if err := fn(&memoryTx{&staged, f}); err != nil {
		return err
	}
	if s.beforeCommit != nil {
		s.beforeCommit(s.state.clone())
	}
	if f == commitFailure {
		return errInjected
	}
	s.state = staged
	if f == lostCommitReply {
		return errUncertain
	}
	return nil
}

type memoryTx struct {
	state *memoryState
	fail  fault
}

func (tx *memoryTx) Lookup(key RetryKey) (Message, bool, error) {
	if tx.fail == lookupFailure {
		return Message{}, false, errInjected
	}
	m, ok := tx.state.retries[key]
	return m, ok, nil
}
func (tx *memoryTx) LastMessageID() (int64, error) {
	if tx.fail == idReadFailure {
		return 0, errInjected
	}
	return tx.state.lastID, nil
}
func (tx *memoryTx) LastCursor(account uint32) (int64, error) {
	if tx.fail == cursorReadFailure {
		return 0, errInjected
	}
	return tx.state.tails[account], nil
}
func (tx *memoryTx) Record(m Message, refs []FeedReference) error {
	if _, exists := tx.state.retries[m.Key]; exists || m.ID != tx.state.lastID+1 {
		return errors.New("fake store overwrite or invalid allocation")
	}
	tx.state.messages = append(tx.state.messages, m)
	tx.state.lastID = m.ID
	if tx.fail == partialRecordFailure {
		return errInjected
	}
	tx.state.retries[m.Key] = m
	for _, ref := range refs {
		if ref.MessageID != m.ID || ref.Cursor != tx.state.tails[ref.AccountID]+1 {
			return errors.New("fake store invalid feed allocation")
		}
		for _, old := range tx.state.feeds[ref.AccountID] {
			if old.MessageID == ref.MessageID {
				return errors.New("fake store duplicate account/message")
			}
		}
		tx.state.tails[ref.AccountID] = ref.Cursor
		tx.state.feeds[ref.AccountID] = append(tx.state.feeds[ref.AccountID], ref)
		if tx.fail == partialFeedFailure {
			return errInjected
		}
	}
	return nil
}
