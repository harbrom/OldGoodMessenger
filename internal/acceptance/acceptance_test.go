package acceptance

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var alice = TrustedContext{AccountID: 100001, WriterID: 1}
var bob = TrustedContext{AccountID: 100002, WriterID: 2}
var carol = TrustedContext{AccountID: 100003, WriterID: 3}

func allow(TrustedContext, Request) bool { return true }
func acceptor(t *testing.T, store Store, policy Policy) *Acceptor {
	t.Helper()
	a, err := New(store, policy)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func accept(t *testing.T, a *Acceptor, who TrustedContext, req Request) Result {
	t.Helper()
	result, err := a.Accept(who, req)
	if err != nil || result.ClientSeq != req.ClientSeq || result.MessageID <= 0 {
		t.Fatalf("unexpected acceptance: %+v, %v", result, err)
	}
	return result
}
func unchanged(t *testing.T, store *memoryStore, before memoryState) {
	t.Helper()
	if !reflect.DeepEqual(before, store.snapshot()) {
		t.Fatal("committed state changed")
	}
}
func requireError(t *testing.T, a *Acceptor, who TrustedContext, req Request, want error) {
	t.Helper()
	result, err := a.Accept(who, req)
	if !errors.Is(err, want) || result != (Result{}) {
		t.Fatalf("got result %+v, error %v; want zero result and %v", result, err, want)
	}
}

func TestFirstAcceptanceAndExactRetry(t *testing.T) {
	store := newMemoryStore()
	calls := 0
	permitted := true
	a := acceptor(t, store, func(TrustedContext, Request) bool { calls++; return permitted })
	req := Request{1, bob.AccountID, "\u041f\u0440\u0438\u0432\u0456\u0442\x00\n"}
	first := accept(t, a, alice, req)
	want := emptyState()
	want.lastID = 1
	m := Message{1, RetryKey{alice.AccountID, alice.WriterID, 1}, bob.AccountID, req.Text}
	want.messages = []Message{m}
	want.retries[m.Key] = m
	for _, id := range []uint32{alice.AccountID, bob.AccountID} {
		want.tails[id] = 1
		want.feeds[id] = []FeedReference{{id, 1, 1}}
	}
	unchanged(t, store, want)
	permitted = false
	if got := accept(t, a, alice, req); got != first {
		t.Fatal("retry changed identity")
	}
	if calls != 1 {
		t.Fatal("accepted retry repeated policy")
	}
	unchanged(t, store, want)
	// Returned snapshots and caller request values cannot mutate immutable records.
	copy := store.snapshot()
	copy.messages[0].Text = "changed"
	delete(copy.retries, m.Key)
	copy.feeds[alice.AccountID][0].Cursor = 99
	req.Text = "changed"
	unchanged(t, store, want)
}

func TestConflictsPreserveOriginal(t *testing.T) {
	store := newMemoryStore()
	calls := 0
	a := acceptor(t, store, func(TrustedContext, Request) bool { calls++; return true })
	original := Request{1, bob.AccountID, "\u00e9"}
	accept(t, a, alice, original)
	before := store.snapshot()
	for _, req := range []Request{{1, bob.AccountID, "changed"}, {1, carol.AccountID, original.Text}, {1, bob.AccountID, "e\u0301"}} {
		requireError(t, a, alice, req, ErrConflict)
		unchanged(t, store, before)
	}
	if calls != 1 {
		t.Fatal("conflict evaluated policy")
	}
	if got := accept(t, a, alice, Request{2, bob.AccountID, "next"}); got.MessageID != 2 {
		t.Fatal("conflict advanced IDs")
	}
}

func TestPolicyRejectionsAndOfflineRecipient(t *testing.T) {
	for _, reason := range []string{"missing", "blocked", "suspended", "generic"} {
		t.Run(reason, func(t *testing.T) {
			store := newMemoryStore()
			before := store.snapshot()
			permitted := false
			a := acceptor(t, store, func(TrustedContext, Request) bool { return permitted })
			req := Request{1, bob.AccountID, "test"}
			requireError(t, a, alice, req, ErrRejected)
			unchanged(t, store, before)
			permitted = true
			if got := accept(t, a, alice, req); got.MessageID != 1 {
				t.Fatal("rejection reserved a retry key or ID")
			}
		})
	}
	store := newMemoryStore()
	// The injected existence/policy decision deliberately has no presence input.
	exists := map[uint32]bool{bob.AccountID: true}
	online := map[uint32]bool{bob.AccountID: false}
	a := acceptor(t, store, func(_ TrustedContext, req Request) bool { return exists[req.Recipient] })
	if online[bob.AccountID] {
		t.Fatal("offline fixture is online")
	}
	accept(t, a, alice, Request{1, bob.AccountID, "offline"})
	before := store.snapshot()
	requireError(t, a, alice, Request{2, carol.AccountID, "missing"}, ErrRejected)
	unchanged(t, store, before)
}

func TestAccountLocalFeedsAndSelfMessaging(t *testing.T) {
	store := newMemoryStore()
	a := acceptor(t, store, allow)
	accept(t, a, alice, Request{1, bob.AccountID, "one"})
	accept(t, a, carol, Request{1, bob.AccountID, "two"})
	accept(t, a, bob, Request{1, alice.AccountID, "three"})
	self := Request{2, alice.AccountID, "self"}
	first := accept(t, a, alice, self)
	before := store.snapshot()
	if accept(t, a, alice, self) != first {
		t.Fatal("self retry changed ID")
	}
	unchanged(t, store, before)
	want := map[uint32][]FeedReference{
		alice.AccountID: {{alice.AccountID, 1, 1}, {alice.AccountID, 2, 3}, {alice.AccountID, 3, 4}},
		bob.AccountID:   {{bob.AccountID, 1, 1}, {bob.AccountID, 2, 2}, {bob.AccountID, 3, 3}},
		carol.AccountID: {{carol.AccountID, 1, 2}},
	}
	if !reflect.DeepEqual(before.feeds, want) || len(before.messages) != 4 || len(before.retries) != 4 {
		t.Fatalf("incorrect feed state: %+v", before)
	}
}

func TestRetryKeyScopes(t *testing.T) {
	store := newMemoryStore()
	a := acceptor(t, store, allow)
	contexts := []TrustedContext{alice, {alice.AccountID, 2}, {bob.AccountID, 1}}
	for i, who := range contexts {
		req := Request{1, carol.AccountID, "same sequence"}
		first := accept(t, a, who, req)
		if first.MessageID != int64(i+1) || accept(t, a, who, req) != first {
			t.Fatal("retry namespace collision")
		}
	}
}

func TestInvalidContextAndRequests(t *testing.T) {
	good := Request{1, bob.AccountID, "A"}
	type invalidCase struct {
		name string
		who  TrustedContext
		req  Request
	}
	cases := []invalidCase{
		{"zero account", TrustedContext{0, 1}, good}, {"short account", TrustedContext{99999, 1}, good},
		{"long account", TrustedContext{1000000000, 1}, good},
		{"zero writer", TrustedContext{alice.AccountID, 0}, good}, {"negative writer", TrustedContext{alice.AccountID, -1}, good},
	}
	for _, id := range []uint32{0, 99999, 1000000000, 0xffffffff} {
		r := good
		r.Recipient = id
		cases = append(cases, invalidCase{"invalid recipient", alice, r})
	}
	for _, seq := range []int64{0, -1} {
		r := good
		r.ClientSeq = seq
		cases = append(cases, invalidCase{"invalid sequence", alice, r})
	}
	for _, text := range []string{"", strings.Repeat("a", 501), strings.Repeat("\U0001f642", 501),
		string([]byte{0xff}), string([]byte{0xc0, 0x80}), string([]byte{0xed, 0xa0, 0x80}), string([]byte{0xf4, 0x90, 0x80, 0x80}), string([]byte{0xd0})} {
		r := good
		r.Text = text
		cases = append(cases, invalidCase{"invalid Unicode or length", alice, r})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryStore()
			before := store.snapshot()
			calls := 0
			a := acceptor(t, store, func(TrustedContext, Request) bool { calls++; return true })
			requireError(t, a, tc.who, tc.req, ErrInvalid)
			unchanged(t, store, before)
			if calls != 0 {
				t.Fatal("invalid request reached policy")
			}
		})
	}
	if _, err := New(nil, allow); err != ErrInvalid {
		t.Fatal("nil store accepted")
	}
	if _, err := New(newMemoryStore(), nil); err != ErrInvalid {
		t.Fatal("nil policy accepted")
	}
}

func TestApprovedBoundaries(t *testing.T) {
	store := newMemoryStore()
	a := acceptor(t, store, allow)
	for i, text := range []string{"A", strings.Repeat("a", 500), strings.Repeat("\U0001f642", 500), strings.Repeat("e\u0301", 250), "\x00\nZa\u017c\u00f3\u0142\u0107"} {
		req := Request{int64(i + 1), 999999999, text}
		accept(t, a, TrustedContext{100000, 1<<63 - 1}, req)
		got := store.snapshot().messages[i]
		if got.Text != text {
			t.Fatal("message bytes changed")
		}
	}
	accept(t, a, alice, Request{1<<63 - 1, bob.AccountID, "maximum sequence"})
}

func TestTransactionFailuresAndUncertainCommit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		point fault
	}{
		{"retry lookup", lookupFailure}, {"message ID read", idReadFailure}, {"cursor read", cursorReadFailure},
		{"partial record", partialRecordFailure}, {"partial feed", partialFeedFailure}, {"commit", commitFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryStore()
			a := acceptor(t, store, allow)
			accept(t, a, alice, Request{1, bob.AccountID, "seed"})
			before := store.snapshot()
			req := Request{2, bob.AccountID, "next"}
			store.inject(tc.point)
			requireError(t, a, alice, req, errInjected)
			unchanged(t, store, before)
			if got := accept(t, a, alice, req); got.MessageID != 2 {
				t.Fatal("failed transaction leaked an allocation")
			}
		})
	}
	store := newMemoryStore()
	calls := 0
	a := acceptor(t, store, func(TrustedContext, Request) bool { calls++; return true })
	req := Request{1, bob.AccountID, "uncertain"}
	store.inject(lostCommitReply)
	requireError(t, a, alice, req, errUncertain)
	before := store.snapshot()
	if before.lastID != 1 || len(before.messages) != 1 || len(before.retries) != 1 || len(before.feeds[alice.AccountID]) != 1 || len(before.feeds[bob.AccountID]) != 1 {
		t.Fatal("uncertain commit was partial")
	}
	if got := accept(t, a, alice, req); got != (Result{1, 1}) || calls != 1 {
		t.Fatal("uncertain retry did not recover original acceptance")
	}
	unchanged(t, store, before)
}

func TestAllocationBounds(t *testing.T) {
	for _, scope := range []string{"message", "sender", "recipient"} {
		for _, last := range []int64{-1, 1<<63 - 1} {
			store := newMemoryStore()
			switch scope {
			case "message":
				store.state.lastID = last
			case "sender":
				store.state.tails[alice.AccountID] = last
			case "recipient":
				store.state.tails[bob.AccountID] = last
			}
			before := store.snapshot()
			want := ErrExhausted
			if last < 0 {
				want = ErrStoreState
			}
			requireError(t, acceptor(t, store, allow), alice, Request{1, bob.AccountID, "bounds"}, want)
			unchanged(t, store, before)
		}
	}
	store := newMemoryStore()
	store.state.lastID = 1<<63 - 2
	store.state.tails[alice.AccountID] = 1<<63 - 2
	store.state.tails[bob.AccountID] = 1<<63 - 2
	a := acceptor(t, store, allow)
	req := Request{1, bob.AccountID, "last"}
	first := accept(t, a, alice, req)
	if first.MessageID != 1<<63-1 {
		t.Fatal("maximum allocation not usable")
	}
	if accept(t, a, alice, req) != first {
		t.Fatal("retry required new allocations")
	}
	before := store.snapshot()
	requireError(t, a, alice, Request{2, bob.AccountID, "overflow"}, ErrExhausted)
	unchanged(t, store, before)
}

func TestNoAcceptanceBeforeCommit(t *testing.T) {
	store := newMemoryStore()
	entered := make(chan memoryState, 1)
	release := make(chan struct{})
	store.beforeCommit = func(committed memoryState) { entered <- committed; <-release }
	a := acceptor(t, store, allow)
	finished := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := a.Accept(alice, Request{1, bob.AccountID, "gated"})
		finished <- struct {
			result Result
			err    error
		}{result, err}
	}()
	var once sync.Once
	defer once.Do(func() { close(release) })
	select {
	case committed := <-entered:
		if !reflect.DeepEqual(committed, emptyState()) {
			t.Fatal("staged writes published before commit")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("transaction did not reach commit gate")
	}
	select {
	case <-finished:
		t.Fatal("acceptance escaped before commit")
	default:
	}
	once.Do(func() { close(release) })
	select {
	case got := <-finished:
		if got.err != nil || got.result != (Result{1, 1}) {
			t.Fatal(got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("commit did not finish")
	}
}

func TestConcurrentIdenticalRetries(t *testing.T) {
	store := newMemoryStore()
	var calls atomic.Int32
	policy := func(TrustedContext, Request) bool { calls.Add(1); return true }
	acceptors := []*Acceptor{acceptor(t, store, policy), acceptor(t, store, policy)}
	const n = 16
	start := make(chan struct{})
	results := make(chan Result, n)
	failures := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r, err := acceptors[i%2].Accept(alice, Request{1, bob.AccountID, "same"})
			results <- r
			failures <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	for r := range results {
		if r != (Result{1, 1}) {
			t.Fatal("identical retry changed identity")
		}
	}
	s := store.snapshot()
	if calls.Load() != 1 || len(s.messages) != 1 || len(s.retries) != 1 || s.lastID != 1 || s.tails[alice.AccountID] != 1 || s.tails[bob.AccountID] != 1 {
		t.Fatal("concurrent retry mutated allocations")
	}
}

func TestConcurrentConflictingRetries(t *testing.T) {
	store := newMemoryStore()
	attempted := make(chan struct{}, 2)
	store.attempted = attempted
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.beforeCommit = func(memoryState) { once.Do(func() { close(entered); <-release }) }
	a := acceptor(t, store, allow)
	b := acceptor(t, store, allow)
	type outcome struct {
		result Result
		err    error
	}
	first := make(chan outcome, 1)
	second := make(chan outcome, 1)
	go func() { r, err := a.Accept(alice, Request{1, bob.AccountID, "winner"}); first <- outcome{r, err} }()
	var cleanup sync.Once
	defer cleanup.Do(func() { close(release) })
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first transaction did not enter")
	}
	<-attempted // The winning call is already inside its commit gate.
	go func() {
		r, err := b.Accept(alice, Request{1, carol.AccountID, "conflict"})
		second <- outcome{r, err}
	}()
	select {
	case <-attempted: // The losing call reached the store while the first still holds its transaction.
	case <-time.After(3 * time.Second):
		t.Fatal("conflicting retry did not reach transaction boundary")
	}
	cleanup.Do(func() { close(release) })
	winner, loser := <-first, <-second
	if winner.err != nil || winner.result != (Result{1, 1}) || loser.err != ErrConflict || loser.result != (Result{}) {
		t.Fatalf("unexpected concurrent outcomes: %+v, %+v", winner, loser)
	}
	s := store.snapshot()
	if len(s.messages) != 1 || s.messages[0].Text != "winner" || s.tails[carol.AccountID] != 0 {
		t.Fatal("conflict overwrote winner or created a recipient feed")
	}
}

func TestConcurrentDistinctCommittedOrder(t *testing.T) {
	store := newMemoryStore()
	a := acceptor(t, store, allow)
	const n = 16
	start := make(chan struct{})
	results := make(chan Result, n)
	failures := make(chan error, n)
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(seq int64) {
			defer wg.Done()
			<-start
			r, err := a.Accept(alice, Request{seq, bob.AccountID, "distinct"})
			results <- r
			failures <- err
		}(int64(i))
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	s := store.snapshot()
	seen := make(map[int64]bool)
	if len(s.messages) != n || len(s.retries) != n || s.lastID != n {
		t.Fatal("lost distinct acceptance")
	}
	for _, m := range s.messages {
		index := len(seen)
		if m.ID != int64(index+1) {
			t.Fatal("IDs not in committed insertion order")
		}
		seen[m.ID] = true
		for _, account := range []uint32{alice.AccountID, bob.AccountID} {
			if s.feeds[account][index] != (FeedReference{account, m.ID, m.ID}) {
				t.Fatal("feed order disagrees with commit order")
			}
		}
	}
	for r := range results {
		if !seen[r.MessageID] || s.messages[r.MessageID-1].Key.ClientSeq != r.ClientSeq {
			t.Fatal("result does not identify committed message")
		}
		delete(seen, r.MessageID)
	}
	if len(seen) != 0 {
		t.Fatal("missing result")
	}
}
