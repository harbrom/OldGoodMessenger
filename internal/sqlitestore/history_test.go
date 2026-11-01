package sqlitestore

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
)

func syncRequest(after, through int64, limit byte) deliveryv1.Frame {
	return deliveryv1.Frame{Kind: deliveryv1.Sync, After: after, Through: through, Limit: limit}
}
func historyPage(t *testing.T, store historysync.Store, account uint32, req deliveryv1.Frame) []deliveryv1.Frame {
	t.Helper()
	service, err := historysync.New(store)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := service.Page(historysync.TrustedContext{AccountID: account}, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 1 || len(frames) > int(req.Limit)+1 || frames[len(frames)-1].Kind != deliveryv1.End {
		t.Fatal("invalid page shape")
	}
	for _, f := range frames {
		b, err := f.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := deliveryv1.Decode(b)
		if err != nil || decoded != f {
			t.Fatal("invalid ITEM/END codec round trip")
		}
	}
	return frames
}
func expectEnd(t *testing.T, frames []deliveryv1.Frame, next, through int64) {
	t.Helper()
	if got := frames[len(frames)-1]; got != (deliveryv1.Frame{Kind: deliveryv1.End, Next: next, Through: through}) {
		t.Fatalf("wrong END: %+v", got)
	}
}
func seedHistory(t *testing.T, count int) *Store {
	t.Helper()
	s := opened(t, filepath.Join(t.TempDir(), "history.sqlite"))
	a := newAcceptor(t, s, nil)
	for i := 1; i <= count; i++ {
		accepted(t, a, alice, int64(i), bob.AccountID, "message")
	}
	return s
}

func TestHistoryAcceptCloseReopenSyncIntegration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen-history.sqlite")
	s := opened(t, path)
	a := newAcceptor(t, s, nil)
	text := "ASCII\x00\n\u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \U0001f642"
	accepted(t, a, carol, 1, bob.AccountID, "not Alice's message")
	original := accepted(t, a, alice, 17, bob.AccountID, text)
	accepted(t, a, alice, 18, alice.AccountID, "self")
	accepted(t, a, bob, 1, alice.AccountID, "incoming reply")
	before := snapshot(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = opened(t, path)
	sameState(t, s, before)
	got := historyPage(t, s, alice.AccountID, syncRequest(0, 0, 8))
	want := []deliveryv1.Frame{
		{Kind: deliveryv1.Item, Cursor: 1, MessageID: 2, Sender: alice.AccountID, Recipient: bob.AccountID, WriterID: alice.WriterID, ClientSeq: 17, Text: text},
		{Kind: deliveryv1.Item, Cursor: 2, MessageID: 3, Sender: alice.AccountID, Recipient: alice.AccountID, WriterID: alice.WriterID, ClientSeq: 18, Text: "self"},
		{Kind: deliveryv1.Item, Cursor: 3, MessageID: 4, Sender: bob.AccountID, Recipient: alice.AccountID, WriterID: bob.WriterID, ClientSeq: 1, Text: "incoming reply"},
		{Kind: deliveryv1.End, Next: 3, Through: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complete reopened ITEM fields differ: %+v", got)
	}
	bobPage := historyPage(t, s, bob.AccountID, syncRequest(1, 0, 8))
	expectEnd(t, bobPage, 3, 3)
	if len(bobPage) != 3 || bobPage[0].Cursor != 2 || bobPage[0].MessageID != 2 || bobPage[0].Text != text || bobPage[1].MessageID != 4 {
		t.Fatal("recipient history/cursors conflated or self-message leaked")
	}
	unrelated := historyPage(t, s, 999999999, syncRequest(0, 0, 8))
	if len(unrelated) != 1 {
		t.Fatal("unrelated account saw messages")
	}
	expectEnd(t, unrelated, 0, 0)
	// Retry identity still resolves after read-only synchronization and reopening.
	if retry := accepted(t, newAcceptor(t, s, func(acceptance.TrustedContext, acceptance.Request) bool {
		t.Fatal("accepted retry reevaluated policy")
		return false
	}), alice, 17, bob.AccountID, text); retry != original {
		t.Fatal("retry ID changed")
	}
	sameState(t, s, before)
}

func TestHistoryPaginationAndReplay(t *testing.T) {
	for _, count := range []int{0, 1, 2, 8, 9, 16} {
		s := seedHistory(t, count)
		before := snapshot(t, s)
		for _, limit := range []byte{1, 2, 8} {
			after, through := int64(0), int64(0)
			for calls := 0; ; calls++ {
				if calls > count {
					t.Fatal("pagination did not terminate")
				}
				frames := historyPage(t, s, alice.AccountID, syncRequest(after, through, limit))
				n := count - int(after)
				if n > int(limit) {
					n = int(limit)
				}
				if len(frames) != n+1 {
					t.Fatal("wrong page item count")
				}
				for i, f := range frames[:n] {
					if f.Cursor != after+int64(i)+1 || f.MessageID != f.Cursor {
						t.Fatal("wrong order or exclusive/inclusive bounds")
					}
				}
				next := after + int64(n)
				expectEnd(t, frames, next, int64(count))
				if next == int64(count) {
					break
				}
				after, through = next, int64(count)
			}
		}
		historyPage(t, s, alice.AccountID, syncRequest(0, int64(count), 8))
		sameState(t, s, before)
	}
}

func TestHistoryCursorGapsAndSnapshotContinuation(t *testing.T) {
	s := seedHistory(t, 3)
	// Controlled sparse-cursor fixture; no deletion or allocation behavior added.
	// Move the largest cursor first to avoid transient primary-key collisions.
	for cursor := int64(3); cursor >= 1; cursor-- {
		exec(t, s, "UPDATE feeds SET cursor=? WHERE account_id=? AND cursor=?", cursor*2, alice.AccountID, cursor)
	}
	exec(t, s, "UPDATE account_cursors SET cursor=7 WHERE account_id=?", alice.AccountID)
	before := snapshot(t, s)
	first := historyPage(t, s, alice.AccountID, syncRequest(0, 0, 2))
	expectEnd(t, first, 4, 7)
	if first[0].Cursor != 2 || first[1].Cursor != 4 {
		t.Fatal("sparse cursors changed")
	}
	last := historyPage(t, s, alice.AccountID, syncRequest(4, 7, 1))
	expectEnd(t, last, 7, 7)
	if len(last) != 2 || last[0].Cursor != 6 {
		t.Fatal("full terminal page did not bridge tail gap")
	}
	gap := historyPage(t, s, alice.AccountID, syncRequest(4, 5, 8))
	expectEnd(t, gap, 5, 5)
	if len(gap) != 1 {
		t.Fatal("gap included a record")
	}
	sameState(t, s, before)
	accepted(t, newAcceptor(t, s, nil), alice, 4, bob.AccountID, "new after H")
	old := historyPage(t, s, alice.AccountID, syncRequest(4, 7, 8))
	expectEnd(t, old, 7, 7)
	if len(old) != 2 {
		t.Fatal("old snapshot included new commit")
	}
	fresh := historyPage(t, s, alice.AccountID, syncRequest(7, 0, 8))
	expectEnd(t, fresh, 8, 8)
	if len(fresh) != 2 || fresh[0].Text != "new after H" {
		t.Fatal("fresh snapshot missed new commit")
	}
}

func TestHistoryReadSnapshotAllowsIndependentCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal-snapshot.sqlite")
	readerStore := opened(t, path)
	writerStore := opened(t, path)
	a := newAcceptor(t, writerStore, nil)
	accepted(t, a, alice, 1, bob.AccountID, "before 1")
	accepted(t, a, alice, 2, bob.AccountID, "before 2")
	captured := make(chan struct{})
	resume := make(chan struct{})
	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	var cleanup sync.Once
	defer cleanup.Do(func() { close(resume) })
	go func() {
		readDone <- readerStore.View(alice.AccountID, func(r historysync.Reader) error {
			tail, err := r.Tail()
			if err != nil {
				return err
			}
			if tail != 2 {
				return errors.New("unexpected initial tail")
			}
			close(captured)
			<-resume
			// Raw transactional reads prove actual SQLite isolation, not just a
			// cached Tail or WHERE cursor<=oldH filtering out newer records.
			tx := r.(*historyReader).tx
			var rawTail, count int64
			if err := tx.QueryRow("SELECT cursor FROM account_cursors WHERE account_id=?", alice.AccountID).Scan(&rawTail); err != nil {
				return err
			}
			if err := tx.QueryRow("SELECT count(*) FROM messages").Scan(&count); err != nil {
				return err
			}
			if rawTail != 2 || count != 2 {
				return errors.New("read view saw commit halfway through snapshot")
			}
			rows, err := r.Records(0, tail, 9)
			if err != nil {
				return err
			}
			if len(rows) != 2 {
				return errors.New("wrong old-snapshot records")
			}
			return nil
		})
	}()
	select {
	case <-captured:
	case <-time.After(3 * time.Second):
		t.Fatal("read view did not capture tail")
	}
	go func() {
		_, err := a.Accept(alice, acceptance.Request{ClientSeq: 3, Recipient: bob.AccountID, Text: "committed while read is open"})
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pinned read view blocked independent WAL writer")
	}
	cleanup.Do(func() { close(resume) })
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read view did not release transaction")
	}
	fresh := historyPage(t, readerStore, alice.AccountID, syncRequest(0, 0, 8))
	expectEnd(t, fresh, 3, 3)
	if len(fresh) != 4 || fresh[2].Text != "committed while read is open" {
		t.Fatal("fresh view did not see independent commit")
	}
	if err := verifyConfiguration(writerStore.db); err != nil {
		t.Fatal("writer guarantees changed:", err)
	}
}

func TestHistoryBoundedIndexedQuery(t *testing.T) {
	s := seedHistory(t, 12)
	if err := s.View(alice.AccountID, func(r historysync.Reader) error {
		for _, max := range []int{1, 2, 9} {
			rows, err := r.Records(1, 12, max)
			if err != nil {
				return err
			}
			if len(rows) != max || rows[0].Reference.Cursor != 2 {
				return errors.New("SQL LIMIT or order not honored")
			}
		}
		tx := r.(*historyReader).tx
		plan, err := tx.Query("EXPLAIN QUERY PLAN "+historyQuery, alice.AccountID, 1, 12, 9)
		if err != nil {
			return err
		}
		defer plan.Close()
		var details []string
		for plan.Next() {
			var id, parent, unused int
			var detail string
			if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
				return err
			}
			details = append(details, detail)
		}
		if err := plan.Err(); err != nil {
			return err
		}
		joined := strings.Join(details, "\n")
		if !strings.Contains(joined, "sqlite_autoindex_feeds_1") || strings.Contains(joined, "TEMP B-TREE") {
			return errors.New("history query did not use cursor primary-key access without sorting")
		}
		t.Log(joined)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryInvalidInputsAndClosedStore(t *testing.T) {
	s := seedHistory(t, 2)
	before := snapshot(t, s)
	for _, account := range []uint32{0, 99999, 1000000000} {
		if err := s.View(account, func(historysync.Reader) error { t.Fatal("invalid account reached callback"); return nil }); err != historysync.ErrInvalid {
			t.Fatal("invalid account accepted")
		}
	}
	if err := s.View(alice.AccountID, nil); err != historysync.ErrInvalid {
		t.Fatal("nil callback accepted")
	}
	service, err := historysync.New(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []deliveryv1.Frame{syncRequest(-1, 0, 1), syncRequest(0, 3, 1), syncRequest(3, 0, 1), syncRequest(2, 1, 1), syncRequest(0, 0, 0), syncRequest(0, 0, 9)} {
		frames, err := service.Page(historysync.TrustedContext{AccountID: alice.AccountID}, req)
		if err == nil || frames != nil {
			t.Fatal("invalid bounds/limit exposed a page")
		}
	}
	if err := s.View(alice.AccountID, func(r historysync.Reader) error {
		for _, max := range []int{0, 10} {
			if rows, err := r.Records(0, 2, max); err != historysync.ErrInvalid || rows != nil {
				return errors.New("unbounded read accepted")
			}
		}
		for _, bounds := range [][2]int64{{-1, 2}, {2, 1}, {0, 3}} {
			if rows, err := r.Records(bounds[0], bounds[1], 1); err != historysync.ErrCursorInvalid || rows != nil {
				return errors.New("invalid reader bounds accepted")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sameState(t, s, before)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.View(alice.AccountID, func(historysync.Reader) error { t.Fatal("closed database reached callback"); return nil }); err == nil {
		t.Fatal("closed store read succeeded")
	}
	if frames, err := service.Page(historysync.TrustedContext{AccountID: alice.AccountID}, syncRequest(0, 0, 8)); err == nil || frames != nil {
		t.Fatal("closed store exposed page")
	}
}

// Test-only wrapper forces a real SQL query failure after ending the read
// transaction. It introduces no production fault hooks or persistent mutation.
type endedView struct{ store *Store }

func (v endedView) View(account uint32, fn func(historysync.Reader) error) error {
	return v.store.View(account, func(r historysync.Reader) error { return fn(endedReader{r, r.(*historyReader).tx}) })
}

type endedReader struct {
	historysync.Reader
	tx *sql.Tx
}

func (r endedReader) Records(after, through int64, max int) ([]historysync.Record, error) {
	if err := r.tx.Rollback(); err != nil {
		return nil, err
	}
	return r.Reader.Records(after, through, max)
}

func TestHistoryErrorsCleanupAndNoPartialPage(t *testing.T) {
	s := seedHistory(t, 3)
	before := snapshot(t, s)
	calls := 0
	if err := s.View(alice.AccountID, func(r historysync.Reader) error {
		calls++
		if _, err := r.Records(0, 3, 2); err != nil {
			return err
		}
		return errInjected
	}); err != errInjected || calls != 1 {
		t.Fatal("callback error lost or callback repeated")
	}
	if frames, err := historysyncPageError(endedView{s}); err == nil || frames != nil {
		t.Fatal("query error exposed partial page")
	}
	sameState(t, s, before)
	// Scan two valid joined rows, then encounter a dangling reference in a
	// deliberately corrupted test fixture. Records must discard the prefix.
	exec(t, s, "PRAGMA foreign_keys=OFF")
	exec(t, s, "DELETE FROM messages WHERE message_id=3")
	exec(t, s, "PRAGMA foreign_keys=ON")
	corrupt := snapshot(t, s)
	if err := s.View(alice.AccountID, func(r historysync.Reader) error {
		rows, err := r.Records(0, 3, 9)
		if err == nil || rows != nil {
			return errors.New("partial scan prefix escaped")
		}
		return errInjected
	}); err != errInjected {
		t.Fatal(err)
	}
	if frames, err := historysyncPageError(s); err == nil || frames != nil {
		t.Fatal("partial failed history page exposed")
	}
	sameState(t, s, corrupt)
	// A fresh view remains usable after both callback and query errors.
	if err := s.View(alice.AccountID, func(r historysync.Reader) error { _, err := r.Tail(); return err }); err != nil {
		t.Fatal("read transaction/connection leaked:", err)
	}
}
func historysyncPageError(s historysync.Store) ([]deliveryv1.Frame, error) {
	service, err := historysync.New(s)
	if err != nil {
		return nil, err
	}
	return service.Page(historysync.TrustedContext{AccountID: alice.AccountID}, syncRequest(0, 0, 8))
}

func TestHistoryCallbackPanicReleasesConnection(t *testing.T) {
	s := seedHistory(t, 1)
	before := snapshot(t, s)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("fixture did not panic")
			}
		}()
		_ = s.View(alice.AccountID, func(historysync.Reader) error { panic("test callback") })
	}()
	historyPage(t, s, alice.AccountID, syncRequest(0, 0, 8))
	sameState(t, s, before)
	accepted(t, newAcceptor(t, s, nil), alice, 2, bob.AccountID, "after read rollback")
}

func TestHistoryIncompatibleSchemaReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incompatible-history.sqlite")
	s := opened(t, path)
	accepted(t, newAcceptor(t, s, nil), alice, 1, bob.AccountID, "retained")
	exec(t, s, "PRAGMA user_version=2")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(path); !errors.Is(err, ErrSchema) {
		if store != nil {
			store.Close()
		}
		t.Fatal("incompatible history database opened")
	}
}
