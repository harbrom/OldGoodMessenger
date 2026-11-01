package clientsync

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
)

const alice uint32 = 100001
const bob uint32 = 100002

func item(cursor, id int64) deliveryv1.Frame {
	return deliveryv1.Frame{Kind: deliveryv1.Item, Cursor: cursor, MessageID: id, Sender: bob, Recipient: alice, WriterID: 7, ClientSeq: id, Text: "\u041f\u0440\u0438\u0432\u0456\u0442 e\u0301 \U0001f642\x00\n"}
}
func end(next, through int64) deliveryv1.Frame {
	return deliveryv1.Frame{Kind: deliveryv1.End, Next: next, Through: through}
}
func session(t *testing.T, store Store, account uint32, limit byte) *Session {
	t.Helper()
	s, err := New(account, limit, store)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func next(t *testing.T, s *Session, after, through int64) deliveryv1.Frame {
	t.Helper()
	req, err := s.Next()
	want := deliveryv1.Frame{Kind: deliveryv1.Sync, After: after, Through: through, Limit: s.limit}
	if err != nil || req != want {
		t.Fatalf("got request %+v, error %v; want %+v", req, err, want)
	}
	return req
}
func handle(t *testing.T, s *Session, frames ...deliveryv1.Frame) {
	t.Helper()
	for _, f := range frames {
		if err := s.Handle(f); err != nil {
			t.Fatalf("frame %+v failed: %v", f, err)
		}
	}
}
func checkpoint(t *testing.T, s *Session, want int64) {
	t.Helper()
	got, err := s.Checkpoint()
	if err != nil || got != want {
		t.Fatalf("checkpoint=%d, error=%v, want=%d", got, err, want)
	}
}
func blocked(t *testing.T, s *Session) {
	t.Helper()
	if _, err := s.Next(); err != ErrRecovery {
		t.Fatal("failed page issued another request")
	}
	if _, err := s.Checkpoint(); err != ErrRecovery {
		t.Fatal("uncertain checkpoint exposed as confirmed")
	}
	if err := s.Handle(end(0, 0)); err != ErrRecovery {
		t.Fatal("failed page kept accepting frames")
	}
}
func server(t *testing.T, count int) *serverStore {
	t.Helper()
	store := newServerStore()
	for i := 1; i <= count; i++ {
		f := item(int64(i*2), int64(100+i*3))
		if i == 5 {
			f.Sender, f.Recipient = alice, alice
		}
		store.add(alice, f)
		if f.Sender != f.Recipient {
			f.Cursor = int64(i + 10)
			store.add(bob, f)
		}
	}
	return store
}
func exchange(t *testing.T, s *Session, source *serverStore, req deliveryv1.Frame) {
	t.Helper()
	srv, err := historysync.New(source)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := srv.Page(historysync.TrustedContext{AccountID: s.account}, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		// Exercise the unchanged codec boundary too, without any socket.
		bytes, err := f.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := deliveryv1.Decode(bytes)
		if err != nil {
			t.Fatal(err)
		}
		handle(t, s, decoded)
	}
}

func TestInitialEmptyAndSingleMessage(t *testing.T) {
	for _, count := range []int{0, 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store := newStore()
			s := session(t, store, alice, 8)
			if !s.NeedsSync() {
				t.Fatal("initial synchronization was optional")
			}
			exchange(t, s, server(t, count), next(t, s, 0, 0))
			checkpoint(t, s, int64(count*2))
			if s.NeedsSync() {
				t.Fatal("terminal page kept requesting continuation")
			}
			_, records := store.snapshot()
			if len(records) != count {
				t.Fatal("wrong message count")
			}
			if count == 1 && records[103] != item(2, 103) {
				t.Fatal("complete ITEM fields not preserved")
			}
			// An explicit later synchronization always captures a fresh H.
			next(t, s, int64(count*2), 0)
		})
	}
}

func TestServerIntegrationPagesAndIndependentCursors(t *testing.T) {
	for _, limit := range []byte{1, 2, 8} {
		for _, account := range []uint32{alice, bob} {
			t.Run(fmt.Sprintf("limit=%d/account=%d", limit, account), func(t *testing.T) {
				source := server(t, 17)
				store := newStore()
				s := session(t, store, account, limit)
				for pages := 0; s.NeedsSync(); pages++ {
					if pages > 18 {
						t.Fatal("continuation did not terminate")
					}
					req, err := s.Next()
					if err != nil {
						t.Fatal(err)
					}
					exchange(t, s, source, req)
				}
				wantTail := source.tails[account]
				checkpoint(t, s, wantTail)
				_, records := store.snapshot()
				if len(records) != len(source.feeds[account]) {
					t.Fatal("lost or duplicated local record")
				}
				for _, row := range source.feeds[account] {
					want := deliveryv1.Frame{Kind: deliveryv1.Item, Cursor: row.Reference.Cursor, MessageID: row.Message.ID, Sender: row.Message.Key.AccountID, Recipient: row.Message.Recipient, WriterID: row.Message.Key.WriterID, ClientSeq: row.Message.Key.ClientSeq, Text: row.Message.Text}
					if records[want.MessageID] != want {
						t.Fatal("stored fields or account-local position changed")
					}
				}
				if account == alice && records[115].Sender != records[115].Recipient {
					t.Fatal("self-message missing")
				}
			})
		}
	}
}

func TestFullPagesSnapshotReuseAndNewHints(t *testing.T) {
	source := server(t, 3)
	store := newStore()
	s := session(t, store, alice, 2)
	exchange(t, s, source, next(t, s, 0, 0))
	checkpoint(t, s, 4)
	handle(t, s, deliveryv1.Frame{Kind: deliveryv1.Changed, Tail: 8}, deliveryv1.Frame{Kind: deliveryv1.Changed, Tail: 7})
	source.add(alice, item(8, 200))
	// A newer hint cannot change the bound of the continuation already selected.
	exchange(t, s, source, next(t, s, 4, 6))
	checkpoint(t, s, 6)
	if !s.NeedsSync() || s.pendingTail != 8 {
		t.Fatal("newest coalesced hint lost")
	}
	exchange(t, s, source, next(t, s, 6, 0))
	checkpoint(t, s, 8)
	if s.NeedsSync() {
		t.Fatal("satisfied hint retained")
	}
	full := session(t, newStore(), alice, 2)
	handle(t, full, deliveryv1.Frame{Kind: deliveryv1.Changed, Tail: 99})
	next(t, full, 0, 0)
	handle(t, full, item(2, 10), item(4, 20), end(4, 4))
	checkpoint(t, full, 4)
	if !full.NeedsSync() {
		t.Fatal("hint beyond full terminal page was lost")
	}
}

func TestInterleavedACKAndCHANGEDDoNotAdvanceCheckpoint(t *testing.T) {
	store := newStore()
	s := session(t, store, alice, 2)
	next(t, s, 0, 0)
	handle(t, s, item(2, 100), deliveryv1.Frame{Kind: deliveryv1.Changed, Tail: 10}, deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 99, MessageID: 999}, deliveryv1.Frame{Kind: deliveryv1.Changed, Tail: 3}, item(5, 300))
	checkpoint(t, s, 0)
	if store.commits != 0 || s.pendingTail != 10 || s.active.count != 2 {
		t.Fatal("interleave changed page/checkpoint")
	}
	handle(t, s, end(7, 7))
	checkpoint(t, s, 7)
	if !s.NeedsSync() {
		t.Fatal("newer interleaved hint ignored")
	}
	extra := session(t, newStore(), alice, 1)
	next(t, extra, 0, 0)
	handle(t, extra, end(0, 0))
	handle(t, extra, deliveryv1.Frame{Kind: deliveryv1.Changed, Tail: 4})
	if !extra.NeedsSync() {
		t.Fatal("idle change hint ignored")
	}
	checkpoint(t, extra, 0)
}

func TestReconnectAndInstanceRecreationReplay(t *testing.T) {
	for _, received := range []int{0, 2} {
		t.Run(fmt.Sprint(received), func(t *testing.T) {
			store := newStore()
			s := session(t, store, alice, 8)
			next(t, s, 0, 0)
			for i := 1; i <= received; i++ {
				handle(t, s, item(int64(i*2), int64(100+i*3)))
			}
			checkpoint(t, s, 0)
			s.Disconnect()
			blocked(t, s)
			s = session(t, store, alice, 8) // Same test store, new reference instance.
			if !s.NeedsSync() {
				t.Fatal("restart required a notification")
			}
			exchange(t, s, server(t, 3), next(t, s, 0, 0))
			checkpoint(t, s, 6)
			_, records := store.snapshot()
			if len(records) != 3 {
				t.Fatal("replay duplicated messages")
			}
		})
	}
	store := newStore()
	s := session(t, store, alice, 1)
	source := server(t, 3)
	exchange(t, s, source, next(t, s, 0, 0))
	checkpoint(t, s, 2)
	next(t, s, 2, 6)
	handle(t, s, item(4, 106))
	if err := s.Reconnect(); err != nil {
		t.Fatal(err)
	}
	// Reconnect abandons the old H, even after a completed earlier page.
	exchange(t, s, source, next(t, s, 2, 0))
	checkpoint(t, s, 4)
	exchange(t, s, source, next(t, s, 4, 6))
	checkpoint(t, s, 6)
	if err := s.Reconnect(); err != nil {
		t.Fatal(err)
	}
	if !s.NeedsSync() {
		t.Fatal("reconnect without CHANGED did not force SYNC")
	}
	exchange(t, s, source, next(t, s, 6, 0))
	checkpoint(t, s, 6)
}

func TestHistoricalReplayNeverRegressesCheckpoint(t *testing.T) {
	source := server(t, 9)
	store := newStore()
	s := session(t, store, alice, 2)
	for s.NeedsSync() {
		req, err := s.Next()
		if err != nil {
			t.Fatal(err)
		}
		exchange(t, s, source, req)
	}
	before, records := store.snapshot()
	req, err := s.Replay(0)
	if err != nil || req.After != 0 || req.Through != 0 {
		t.Fatal("historical replay did not start at requested position")
	}
	exchange(t, s, source, req)
	checkpoint(t, s, before)
	// Retrieval continues at four, independently of checkpoint eighteen.
	exchange(t, s, source, next(t, s, 4, 18))
	checkpoint(t, s, before)
	for s.NeedsSync() {
		req, err := s.Next()
		if err != nil {
			t.Fatal(err)
		}
		exchange(t, s, source, req)
		checkpoint(t, s, before)
	}
	after, got := store.snapshot()
	if after != before || !reflect.DeepEqual(records, got) {
		t.Fatal("historical replay changed records/checkpoint")
	}
}

func TestPageLimitAndItemValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*deliveryv1.Frame)
	}{
		{"zero ID", func(f *deliveryv1.Frame) { f.MessageID = 0 }}, {"zero cursor", func(f *deliveryv1.Frame) { f.Cursor = 0 }},
		{"negative cursor", func(f *deliveryv1.Frame) { f.Cursor = -1 }}, {"zero writer", func(f *deliveryv1.Frame) { f.WriterID = 0 }},
		{"zero sequence", func(f *deliveryv1.Frame) { f.ClientSeq = 0 }}, {"invalid sender", func(f *deliveryv1.Frame) { f.Sender = 0 }},
		{"invalid recipient", func(f *deliveryv1.Frame) { f.Recipient = 99999 }},
		{"nonparticipant", func(f *deliveryv1.Frame) { f.Sender = bob; f.Recipient = bob }},
		{"invalid UTF-8", func(f *deliveryv1.Frame) { f.Text = string([]byte{0xed, 0xa0, 0x80}) }},
		{"501 scalars", func(f *deliveryv1.Frame) { f.Text = strings.Repeat("a", 501) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore()
			s := session(t, store, alice, 1)
			next(t, s, 0, 0)
			f := item(2, 100)
			tc.mutate(&f)
			if err := s.Handle(f); err != ErrProtocol {
				t.Fatalf("invalid ITEM accepted: %v", err)
			}
			cp, records := store.snapshot()
			if cp != 0 || len(records) != 0 || store.commits != 0 {
				t.Fatal("invalid ITEM wrote state")
			}
			blocked(t, s)
		})
	}
	for _, second := range []deliveryv1.Frame{item(2, 100), item(1, 101), item(3, 100), item(3, 99)} {
		store := newStore()
		s := session(t, store, alice, 2)
		next(t, s, 0, 0)
		handle(t, s, item(2, 100))
		if err := s.Handle(second); err != ErrProtocol {
			t.Fatal("duplicate/out-of-order ITEM accepted")
		}
		cp, records := store.snapshot()
		if cp != 0 || len(records) != 1 {
			t.Fatal("rejected ITEM advanced state")
		}
		blocked(t, s)
	}
	store := newStore()
	s := session(t, store, alice, 1)
	next(t, s, 0, 0)
	handle(t, s, item(2, 100))
	if err := s.Handle(item(4, 200)); err != ErrProtocol {
		t.Fatal("extra ITEM accepted")
	}
	if cp, records := store.snapshot(); cp != 0 || len(records) != 1 {
		t.Fatal("over-limit ITEM was stored")
	}
}

func knownPage(t *testing.T) *Session {
	t.Helper()
	s := session(t, newStore(), alice, 2)
	next(t, s, 0, 0)
	handle(t, s, item(2, 100), item(4, 200), end(4, 10))
	next(t, s, 4, 10)
	return s
}
func TestEndAndKnownSnapshotValidation(t *testing.T) {
	cases := []struct {
		name  string
		items []deliveryv1.Frame
		end   deliveryv1.Frame
	}{
		{"wrong H", nil, end(11, 11)}, {"zero H", nil, end(0, 0)},
		{"behind after", nil, end(3, 10)}, {"next beyond H", nil, end(11, 10)},
		{"empty nonterminal", nil, end(5, 10)}, {"short nonterminal", []deliveryv1.Frame{item(6, 300)}, end(6, 10)},
		{"next differs from last", []deliveryv1.Frame{item(6, 300), item(8, 400)}, end(7, 10)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := knownPage(t)
			handle(t, s, tc.items...)
			if err := s.Handle(tc.end); err != ErrProtocol {
				t.Fatalf("bad END accepted: %v", err)
			}
			blocked(t, s)
			cp, _ := s.store.(*memoryStore).snapshot()
			if cp != 4 {
				t.Fatal("bad END committed checkpoint")
			}
		})
	}
	for _, cursor := range []int64{4, 11} {
		s := knownPage(t)
		if err := s.Handle(item(cursor, 300)); err != ErrProtocol {
			t.Fatal("ITEM outside known bounds accepted")
		}
	}
	store := newStore()
	s := session(t, store, alice, 2)
	next(t, s, 0, 0)
	handle(t, s, item(6, 300))
	if err := s.Handle(end(5, 5)); err != ErrProtocol {
		t.Fatal("fresh END did not validate deferred ITEM bound")
	}
	if cp, _ := store.snapshot(); cp != 0 {
		t.Fatal("deferred-bound failure advanced checkpoint")
	}
	// Empty terminal pages may bridge gaps; completeness cannot be inferred.
	s = knownPage(t)
	handle(t, s, end(10, 10))
	checkpoint(t, s, 10)
}

func TestStorageFailuresAndActualCheckpointRecovery(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("item-reply-lost=%v", lost), func(t *testing.T) {
			store := newStore()
			s := session(t, store, alice, 1)
			next(t, s, 0, 0)
			store.failItem = !lost
			store.loseItemReply = lost
			if err := s.Handle(item(2, 103)); err != errInjected {
				t.Fatal("ITEM write failure lost")
			}
			blocked(t, s)
			if cp, _ := store.snapshot(); cp != 0 {
				t.Fatal("ITEM error advanced checkpoint")
			}
			s = session(t, store, alice, 1)
			exchange(t, s, server(t, 1), next(t, s, 0, 0))
			checkpoint(t, s, 2)
			if _, records := store.snapshot(); len(records) != 1 {
				t.Fatal("retry duplicated uncertain ITEM")
			}
		})
	}
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("checkpoint-reply-lost=%v", lost), func(t *testing.T) {
			store := newStore()
			s := session(t, store, alice, 1)
			next(t, s, 0, 0)
			handle(t, s, item(2, 103))
			store.failCheckpoint = !lost
			store.loseCheckpointReply = lost
			if err := s.Handle(end(2, 2)); err != errInjected {
				t.Fatal("checkpoint failure reported success")
			}
			blocked(t, s)
			actual, _ := store.snapshot()
			want := int64(0)
			if lost {
				want = 2
			}
			if actual != want {
				t.Fatal("failure fixture has wrong commit outcome")
			}
			store.failRead = true
			if err := s.Reconnect(); err != errInjected {
				t.Fatal("recovery read failure lost")
			}
			blocked(t, s)
			if err := s.Reconnect(); err != nil {
				t.Fatal(err)
			}
			checkpoint(t, s, want)
			exchange(t, s, server(t, 1), next(t, s, want, 0))
			checkpoint(t, s, 2)
			if _, records := store.snapshot(); len(records) != 1 {
				t.Fatal("checkpoint retry duplicated record")
			}
		})
	}
}

func TestUnexpectedFramesConfigurationAndLocalTransitions(t *testing.T) {
	for _, f := range []deliveryv1.Frame{item(1, 1), end(0, 0), {Kind: deliveryv1.Ack, ClientSeq: 1, MessageID: 1}, {Kind: 0x12}} {
		s := session(t, newStore(), alice, 2)
		if err := s.Handle(f); err != ErrProtocol {
			t.Fatal("unexpected idle frame accepted")
		}
		blocked(t, s)
	}
	store := newStore()
	s := session(t, store, alice, 2)
	next(t, s, 0, 0)
	if _, err := s.Next(); err != ErrOutstanding {
		t.Fatal("second outstanding SYNC allowed")
	}
	if _, err := s.Replay(0); err != ErrOutstanding {
		t.Fatal("replay interrupted active request")
	}
	handle(t, s, end(0, 0))
	for _, after := range []int64{-1, 1} {
		if _, err := s.Replay(after); err != ErrInvalid {
			t.Fatal("invalid historical position accepted")
		}
	}
	for _, tc := range []struct {
		account uint32
		limit   byte
		store   Store
	}{{0, 1, newStore()}, {alice, 0, newStore()}, {alice, 9, newStore()}, {alice, 1, nil}} {
		if _, err := New(tc.account, tc.limit, tc.store); err != ErrInvalid {
			t.Fatal("invalid configuration accepted")
		}
	}
	store.failRead = true
	if _, err := New(alice, 1, store); err != errInjected {
		t.Fatal("initial read failure lost")
	}
	store.checkpoint = -1
	if _, err := New(alice, 1, store); err != ErrStoreState {
		t.Fatal("negative stored checkpoint accepted")
	}
}

func TestItemFailureMidPageRetainsReplayablePrefix(t *testing.T) {
	store := newStore()
	s := session(t, store, alice, 8)
	next(t, s, 0, 0)
	handle(t, s, item(2, 103))
	store.failItem = true
	if err := s.Handle(item(4, 106)); err != errInjected {
		t.Fatal("mid-page failure reported success")
	}
	blocked(t, s)
	if cp, records := store.snapshot(); cp != 0 || len(records) != 1 {
		t.Fatal("failed page lost its prefix or advanced checkpoint")
	}
	if err := s.Reconnect(); err != nil {
		t.Fatal(err)
	}
	exchange(t, s, server(t, 3), next(t, s, 0, 0))
	checkpoint(t, s, 6)
	if _, records := store.snapshot(); len(records) != 3 {
		t.Fatal("prefix replay duplicated local messages")
	}
}

func TestImmutableReplayCanonicalFieldsAndMaximumText(t *testing.T) {
	store := newStore()
	s := session(t, store, alice, 1)
	next(t, s, 0, 0)
	f := item(2, 100)
	f.Text = strings.Repeat("\U0001f642", 500)
	f.After = 999 // Not an ITEM wire field.
	handle(t, s, f, end(2, 2))
	checkpoint(t, s, 2)
	_, records := store.snapshot()
	f.After = 0
	if records[100] != f {
		t.Fatal("wire fields not canonical or exact text changed")
	}
	if _, err := s.Replay(0); err != nil {
		t.Fatal(err)
	}
	f.Text = "changed"
	if err := s.Handle(f); err != errRecordConflict {
		t.Fatal("same ID overwrote immutable message")
	}
	if cp, records := store.snapshot(); cp != 2 || records[100].Text == "changed" {
		t.Fatal("conflicting replay changed retained state")
	}
	// Detect a store rollback instead of silently moving the confirmed checkpoint back.
	store.checkpoint = 1
	if err := s.Reconnect(); err != ErrStoreState {
		t.Fatal("regressing stored checkpoint accepted")
	}
}

func TestMalformedInterleavesAbortWithoutCheckpoint(t *testing.T) {
	for _, f := range []deliveryv1.Frame{{Kind: deliveryv1.Ack, ClientSeq: 0, MessageID: 1}, {Kind: deliveryv1.Changed, Tail: -1}, {Kind: deliveryv1.Failure, RequestOp: deliveryv1.Sync, Reference: 0, Code: deliveryv1.Retryable}} {
		store := newStore()
		s := session(t, store, alice, 1)
		next(t, s, 0, 0)
		if err := s.Handle(f); err != ErrProtocol {
			t.Fatal("unsupported/invalid interleave accepted")
		}
		blocked(t, s)
		if cp, _ := store.snapshot(); cp != 0 || store.commits != 0 {
			t.Fatal("interleave committed checkpoint")
		}
	}
	// Each independent device keeps its own checkpoint, even for the same account.
	source := server(t, 2)
	one := session(t, newStore(), alice, 8)
	two := session(t, newStore(), alice, 8)
	exchange(t, one, source, next(t, one, 0, 0))
	checkpoint(t, one, 4)
	checkpoint(t, two, 0)
	exchange(t, two, source, next(t, two, 0, 0))
	checkpoint(t, two, 4)
}

func TestNoProgressAtDisconnectBeforeEND(t *testing.T) {
	store := newStore()
	s := session(t, store, alice, 8)
	next(t, s, 0, 0)
	handle(t, s, item(2, 100), item(7, 200))
	if store.commits != 0 {
		t.Fatal("ITEM committed a checkpoint")
	}
	checkpoint(t, s, 0)
	s.Disconnect()
	if cp, records := store.snapshot(); cp != 0 || len(records) != 2 {
		t.Fatal("disconnect changed retained records/checkpoint")
	}
	if err := s.Reconnect(); err != nil {
		t.Fatal(err)
	}
	next(t, s, 0, 0)
	handle(t, s, item(2, 100), item(7, 200), end(9, 9))
	checkpoint(t, s, 9)
	if _, records := store.snapshot(); len(records) != 2 {
		t.Fatal("replay appended duplicate rows")
	}
}

// A broken storage reply must not be presented as successful checkpoint commit.
type wrongCheckpointStore struct{ *memoryStore }

func (s wrongCheckpointStore) AdvanceCheckpoint(candidate int64) (int64, error) {
	return candidate - 1, nil
}
func TestInvalidCheckpointReplyRequiresRecovery(t *testing.T) {
	store := newStore()
	s := session(t, wrongCheckpointStore{store}, alice, 1)
	next(t, s, 0, 0)
	handle(t, s, item(2, 100))
	if err := s.Handle(end(2, 2)); !errors.Is(err, ErrStoreState) {
		t.Fatal("invalid checkpoint reply accepted")
	}
	blocked(t, s)
}
