package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ogm.local/firstcontact/internal/acceptance"
)

var alice = acceptance.TrustedContext{AccountID: 100001, WriterID: 1}
var bob = acceptance.TrustedContext{AccountID: 100002, WriterID: 2}
var carol = acceptance.TrustedContext{AccountID: 100003, WriterID: 3}
var errInjected = errors.New("injected transaction/commit-reply failure")

func opened(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func newAcceptor(t *testing.T, store acceptance.Store, policy acceptance.Policy) *acceptance.Acceptor {
	t.Helper()
	if policy == nil {
		policy = func(acceptance.TrustedContext, acceptance.Request) bool { return true }
	}
	a, err := acceptance.New(store, policy)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func accepted(t *testing.T, a *acceptance.Acceptor, who acceptance.TrustedContext, seq int64, to uint32, text string) acceptance.Result {
	t.Helper()
	r, err := a.Accept(who, acceptance.Request{ClientSeq: seq, Recipient: to, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	if r.ClientSeq != seq || r.MessageID <= 0 {
		t.Fatal("invalid acceptance result")
	}
	return r
}
func reject(t *testing.T, a *acceptance.Acceptor, who acceptance.TrustedContext, req acceptance.Request, want error) {
	t.Helper()
	r, err := a.Accept(who, req)
	if r != (acceptance.Result{}) || !errors.Is(err, want) {
		t.Fatalf("result=%+v error=%v, want %v", r, err, want)
	}
}
func exec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

type state struct {
	last     int64
	messages []acceptance.Message
	tails    map[uint32]int64
	feeds    []acceptance.FeedReference
}

func snapshot(t *testing.T, s *Store) state {
	t.Helper()
	out := state{tails: make(map[uint32]int64)}
	if err := s.db.QueryRow("SELECT message_id FROM allocation WHERE singleton=1").Scan(&out.last); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query("SELECT message_id,sender,writer_id,client_seq,recipient,text FROM messages ORDER BY message_id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var m acceptance.Message
		var text []byte
		if err := rows.Scan(&m.ID, &m.Key.AccountID, &m.Key.WriterID, &m.Key.ClientSeq, &m.Recipient, &text); err != nil {
			t.Fatal(err)
		}
		m.Text = string(text)
		out.messages = append(out.messages, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	rows, err = s.db.Query("SELECT account_id,cursor FROM account_cursors ORDER BY account_id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uint32
		var cursor int64
		if err := rows.Scan(&id, &cursor); err != nil {
			t.Fatal(err)
		}
		out.tails[id] = cursor
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	rows, err = s.db.Query("SELECT account_id,cursor,message_id FROM feeds ORDER BY account_id,cursor")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ref acceptance.FeedReference
		if err := rows.Scan(&ref.AccountID, &ref.Cursor, &ref.MessageID); err != nil {
			t.Fatal(err)
		}
		out.feeds = append(out.feeds, ref)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	return out
}
func sameState(t *testing.T, s *Store, want state) {
	t.Helper()
	if got := snapshot(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("state changed: got %+v, want %+v", got, want)
	}
}

func TestPersistenceReopeningAndRetryAssociations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acceptance.sqlite")
	s := opened(t, path)
	a := newAcceptor(t, s, nil)
	text := "ASCII\x00\n\u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \U0001f642"
	first := accepted(t, a, alice, 1, bob.AccountID, text)
	accepted(t, a, carol, 1, bob.AccountID, "offline recipient")
	accepted(t, a, bob, 1, alice.AccountID, "reply")
	accepted(t, a, alice, 2, alice.AccountID, "self")
	accepted(t, a, acceptance.TrustedContext{AccountID: alice.AccountID, WriterID: 2}, 1, bob.AccountID, "second writer")
	before := snapshot(t, s)
	if before.last != 5 || len(before.messages) != 5 || len(before.feeds) != 9 || before.tails[alice.AccountID] != 4 || before.tails[bob.AccountID] != 4 || before.tails[carol.AccountID] != 1 {
		t.Fatalf("incorrect persisted counts: %+v", before)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = opened(t, path)
	sameState(t, s, before)
	var calls atomic.Int32
	denied := newAcceptor(t, s, func(acceptance.TrustedContext, acceptance.Request) bool { calls.Add(1); return false })
	if got := accepted(t, denied, alice, 1, bob.AccountID, text); got != first || calls.Load() != 0 {
		t.Fatal("reopened retry repeated policy or changed ID")
	}
	reject(t, denied, alice, acceptance.Request{ClientSeq: 1, Recipient: bob.AccountID, Text: "changed"}, acceptance.ErrConflict)
	reject(t, denied, alice, acceptance.Request{ClientSeq: 1, Recipient: carol.AccountID, Text: text}, acceptance.ErrConflict)
	sameState(t, s, before)
	if r := accepted(t, newAcceptor(t, s, nil), alice, 3, bob.AccountID, "next"); r.MessageID != 6 {
		t.Fatal("message ID allocation restarted")
	}
	after := snapshot(t, s)
	if after.tails[alice.AccountID] != 5 || after.tails[bob.AccountID] != 5 || after.tails[carol.AccountID] != 1 {
		t.Fatal("account cursors restarted or coupled")
	}
	for _, m := range after.messages {
		var id int64
		if err := s.db.QueryRow("SELECT message_id FROM messages WHERE sender=? AND writer_id=? AND client_seq=?", m.Key.AccountID, m.Key.WriterID, m.Key.ClientSeq).Scan(&id); err != nil || id != m.ID {
			t.Fatal("retry association lost")
		}
	}
}

// Failure wrapping is test-only; the production adapter has no fault hooks.
type intercept struct {
	store     *Store
	after     func(acceptance.Transaction) error
	loseReply bool
}

func (i intercept) Transact(fn func(acceptance.Transaction) error) error {
	err := i.store.Transact(func(tx acceptance.Transaction) error {
		if err := fn(tx); err != nil {
			return err
		}
		if i.after != nil {
			return i.after(tx)
		}
		return nil
	})
	if err == nil && i.loseReply {
		return errInjected
	}
	return err
}
func TestRollbackAndLostCommitReply(t *testing.T) {
	for _, stage := range []string{"callback", "partial feed", "commit"} {
		t.Run(stage, func(t *testing.T) {
			s := opened(t, filepath.Join(t.TempDir(), "rollback.sqlite"))
			base := newAcceptor(t, s, nil)
			accepted(t, base, alice, 1, bob.AccountID, "seed")
			before := snapshot(t, s)
			wrapped := acceptance.Store(s)
			switch stage {
			case "callback":
				wrapped = intercept{store: s, after: func(acceptance.Transaction) error { return errInjected }}
			case "partial feed":
				exec(t, s, "CREATE TEMP TRIGGER fail_feed BEFORE INSERT ON main.feeds WHEN NEW.account_id=100002 BEGIN SELECT RAISE(ABORT,'injected feed write failure'); END")
			case "commit":
				exec(t, s, "CREATE TEMP TABLE commit_fault(message_id INTEGER REFERENCES messages(message_id) DEFERRABLE INITIALLY DEFERRED)")
				wrapped = intercept{store: s, after: func(tx acceptance.Transaction) error {
					_, err := tx.(*transaction).tx.Exec("INSERT INTO commit_fault(message_id) VALUES(999999)")
					return err
				}}
			}
			r, err := newAcceptor(t, wrapped, nil).Accept(alice, acceptance.Request{ClientSeq: 2, Recipient: bob.AccountID, Text: "rollback"})
			if err == nil || r != (acceptance.Result{}) {
				t.Fatal("failed transaction exposed acceptance")
			}
			sameState(t, s, before)
			if stage == "partial feed" {
				exec(t, s, "DROP TRIGGER temp.fail_feed")
			}
			if got := accepted(t, base, alice, 2, bob.AccountID, "retry"); got.MessageID != 2 {
				t.Fatal("failed transaction consumed ID")
			}
			after := snapshot(t, s)
			if after.tails[alice.AccountID] != 2 || after.tails[bob.AccountID] != 2 {
				t.Fatal("failed transaction consumed cursor")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "uncertain.sqlite")
	s := opened(t, path)
	reject(t, newAcceptor(t, intercept{store: s, loseReply: true}, nil), alice, acceptance.Request{ClientSeq: 1, Recipient: bob.AccountID, Text: "uncertain"}, errInjected)
	before := snapshot(t, s)
	if before.last != 1 || len(before.messages) != 1 || len(before.feeds) != 2 {
		t.Fatal("lost reply lost committed state")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = opened(t, path)
	if got := accepted(t, newAcceptor(t, s, func(acceptance.TrustedContext, acceptance.Request) bool { return false }), alice, 1, bob.AccountID, "uncertain"); got.MessageID != 1 {
		t.Fatal("uncertain retry did not find original")
	}
	sameState(t, s, before)
}

func TestPolicyRejectionAndSelfMessage(t *testing.T) {
	s := opened(t, filepath.Join(t.TempDir(), "policy.sqlite"))
	before := snapshot(t, s)
	reject(t, newAcceptor(t, s, func(acceptance.TrustedContext, acceptance.Request) bool { return false }), alice, acceptance.Request{ClientSeq: 1, Recipient: bob.AccountID, Text: "denied"}, acceptance.ErrRejected)
	sameState(t, s, before)
	a := newAcceptor(t, s, nil)
	accepted(t, a, alice, 1, alice.AccountID, "self")
	after := snapshot(t, s)
	if after.last != 1 || len(after.messages) != 1 || len(after.feeds) != 1 || after.feeds[0] != (acceptance.FeedReference{AccountID: alice.AccountID, Cursor: 1, MessageID: 1}) {
		t.Fatal("self-message created duplicate references")
	}
}

func TestConcurrentInstancesDeduplicateAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.sqlite")
	stores := []*Store{opened(t, path), opened(t, path)}
	var policyCalls atomic.Int32
	policy := func(acceptance.TrustedContext, acceptance.Request) bool { policyCalls.Add(1); return true }
	acceptors := []*acceptance.Acceptor{newAcceptor(t, stores[0], policy), newAcceptor(t, stores[1], policy)}
	const n = 12
	type outcome struct {
		r   acceptance.Result
		err error
	}
	run := func(distinct bool) []outcome {
		start := make(chan struct{})
		out := make(chan outcome, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				seq := int64(1)
				if distinct {
					seq = int64(i + 2)
				}
				r, err := acceptors[i%2].Accept(alice, acceptance.Request{ClientSeq: seq, Recipient: bob.AccountID, Text: "concurrent"})
				out <- outcome{r, err}
			}(i)
		}
		close(start)
		wg.Wait()
		close(out)
		var results []outcome
		for result := range out {
			if result.err != nil {
				t.Fatal(result.err)
			}
			results = append(results, result)
		}
		return results
	}
	for _, o := range run(false) {
		if o.r != (acceptance.Result{ClientSeq: 1, MessageID: 1}) {
			t.Fatal("concurrent duplicate got another ID")
		}
	}
	if policyCalls.Load() != 1 {
		t.Fatal("concurrent duplicate repeated policy")
	}
	results := run(true)
	state := snapshot(t, stores[0])
	if state.last != n+1 || len(state.messages) != n+1 || len(state.feeds) != 2*(n+1) || policyCalls.Load() != n+1 {
		t.Fatal("concurrent uniqueness failed")
	}
	for i, m := range state.messages {
		if m.ID != int64(i+1) {
			t.Fatal("nonsequential committed message IDs")
		}
	}
	for _, o := range results {
		if state.messages[o.r.MessageID-1].Key.ClientSeq != o.r.ClientSeq {
			t.Fatal("result mapped to wrong committed message")
		}
	}
	for _, s := range stores {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	sameState(t, opened(t, path), state)
}

func TestImmediateWriterLockAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locking.sqlite")
	one := opened(t, path)
	two := opened(t, path)
	holder, err := one.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	// Shorten only this test connection's wait; production remains fixed at 5s.
	exec(t, two, "PRAGMA busy_timeout=20")
	called := false
	err = two.Transact(func(acceptance.Transaction) error { called = true; return nil })
	if err == nil || called {
		t.Fatal("another instance entered callback without obtaining writer lock")
	}
	if coded, ok := err.(interface{ Code() int }); !ok || coded.Code()&255 != 5 {
		t.Fatalf("expected SQLITE_BUSY at BEGIN, got %v", err)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	exec(t, two, "PRAGMA busy_timeout=5000")
	accepted(t, newAcceptor(t, two, nil), alice, 1, bob.AccountID, "after lock release")
}

func TestConfigurationForeignKeysAndConnectionReplacement(t *testing.T) {
	s := opened(t, filepath.Join(t.TempDir(), "configuration.sqlite"))
	if err := verifyConfiguration(s.db); err != nil {
		t.Fatal(err)
	}
	var version string
	if err := s.db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Log("SQLite runtime", version)
	for _, replacement := range []bool{false, true} {
		if replacement {
			s.db.SetMaxIdleConns(0)
		}
		conn, err := s.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyConfiguration(conn); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(context.Background(), "INSERT INTO feeds(account_id,cursor,message_id) VALUES(100001,1,999)"); err == nil {
			t.Fatal("foreign key violation accepted")
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	s.db.SetMaxIdleConns(1)
	accepted(t, newAcceptor(t, s, nil), alice, 1, bob.AccountID, "new connection")
	exec(t, s, "PRAGMA synchronous=NORMAL")
	if err := s.Transact(func(acceptance.Transaction) error {
		t.Fatal("unsafe connection reached acceptance callback")
		return nil
	}); err != ErrConfiguration {
		t.Fatal("unsafe durability setting silently accepted")
	}
}

func TestUnicodeAndNumericBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boundaries.sqlite")
	s := opened(t, path)
	a := newAcceptor(t, s, nil)
	for i, text := range []string{"A", strings.Repeat("a", 500), strings.Repeat("\U0001f642", 500), strings.Repeat("e\u0301", 250), "\x00\n"} {
		accepted(t, a, acceptance.TrustedContext{AccountID: 100000, WriterID: 1<<63 - 1}, int64(i+1), 999999999, text)
	}
	before := snapshot(t, s)
	for _, text := range []string{"", strings.Repeat("a", 501), strings.Repeat("\U0001f642", 501), string([]byte{0xed, 0xa0, 0x80})} {
		reject(t, a, alice, acceptance.Request{ClientSeq: 10, Recipient: bob.AccountID, Text: text}, acceptance.ErrInvalid)
	}
	reject(t, a, acceptance.TrustedContext{AccountID: 0, WriterID: 1}, acceptance.Request{ClientSeq: 1, Recipient: bob.AccountID, Text: "bad"}, acceptance.ErrInvalid)
	reject(t, a, acceptance.TrustedContext{AccountID: alice.AccountID, WriterID: 0}, acceptance.Request{ClientSeq: 1, Recipient: bob.AccountID, Text: "bad"}, acceptance.ErrInvalid)
	reject(t, a, alice, acceptance.Request{ClientSeq: 0, Recipient: bob.AccountID, Text: "bad"}, acceptance.ErrInvalid)
	reject(t, a, alice, acceptance.Request{ClientSeq: 1, Recipient: 1000000000, Text: "bad"}, acceptance.ErrInvalid)
	sameState(t, s, before)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = opened(t, path)
	sameState(t, s, before)
	accepted(t, newAcceptor(t, s, nil), alice, 1<<63-1, bob.AccountID, "max sequence")
	exec(t, s, "UPDATE allocation SET message_id=? WHERE singleton=1", int64(1<<63-2))
	accepted(t, newAcceptor(t, s, nil), alice, 2, bob.AccountID, "last message ID")
	full := snapshot(t, s)
	reject(t, newAcceptor(t, s, nil), alice, acceptance.Request{ClientSeq: 3, Recipient: bob.AccountID, Text: "overflow"}, acceptance.ErrExhausted)
	sameState(t, s, full)
}

func TestInvalidPathsAndSchemaCompatibility(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"", ":memory:", "file:any?mode=memory", dir, filepath.Join(dir, "missing", "db.sqlite")} {
		if s, err := Open(path); err == nil {
			s.Close()
			t.Fatal("invalid database path opened")
		}
	}
	corrupt := filepath.Join(dir, "not-sqlite")
	if err := os.WriteFile(corrupt, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(corrupt); err == nil {
		s.Close()
		t.Fatal("invalid database contents opened")
	}
	path := filepath.Join(dir, "version.sqlite")
	s := opened(t, path)
	accepted(t, newAcceptor(t, s, nil), alice, 1, bob.AccountID, "retained")
	exec(t, s, "PRAGMA user_version=42")
	before := snapshot(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(path); !errors.Is(err, ErrSchema) {
		if opened != nil {
			opened.Close()
		}
		t.Fatalf("unsupported version accepted: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 42 {
		t.Fatal("unsupported schema version modified")
	}
	if _, err := db.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s = opened(t, path)
	sameState(t, s, before)
	exec(t, s, "ALTER TABLE messages ADD COLUMN unexpected INTEGER")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(path); !errors.Is(err, ErrSchema) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("incompatible version-one schema accepted")
	}
}

func TestCursorExhaustionDoesNotConsumeAllocations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account uint32
	}{{"sender", alice.AccountID}, {"recipient", bob.AccountID}} {
		t.Run(tc.name, func(t *testing.T) {
			s := opened(t, filepath.Join(t.TempDir(), "cursor.sqlite"))
			a := newAcceptor(t, s, nil)
			accepted(t, a, alice, 1, bob.AccountID, "seed")
			exec(t, s, "UPDATE account_cursors SET cursor=? WHERE account_id=?", int64(1<<63-1), tc.account)
			before := snapshot(t, s)
			reject(t, a, alice, acceptance.Request{ClientSeq: 2, Recipient: bob.AccountID, Text: "exhausted cursor"}, acceptance.ErrExhausted)
			sameState(t, s, before)
			exec(t, s, "UPDATE account_cursors SET cursor=1 WHERE account_id=?", tc.account)
			if got := accepted(t, a, alice, 2, bob.AccountID, "after fixture reset"); got.MessageID != 2 {
				t.Fatal("failed cursor allocation consumed message ID")
			}
		})
	}
}

func TestRecordAndDatabaseConstraints(t *testing.T) {
	s := opened(t, filepath.Join(t.TempDir(), "constraints.sqlite"))
	a := newAcceptor(t, s, nil)
	accepted(t, a, alice, 1, bob.AccountID, "original")
	before := snapshot(t, s)
	for _, query := range []string{
		"INSERT INTO messages(message_id,sender,writer_id,client_seq,recipient,text) SELECT 2,sender,writer_id,client_seq,recipient,text FROM messages WHERE message_id=1",
		"INSERT INTO feeds(account_id,cursor,message_id) VALUES(100001,2,1)",
		"INSERT INTO feeds(account_id,cursor,message_id) VALUES(100001,1,1)",
		"INSERT INTO messages(message_id,sender,writer_id,client_seq,recipient,text) VALUES(2,1,1,2,100002,x'41')",
		"INSERT INTO messages(message_id,sender,writer_id,client_seq,recipient,text) VALUES(2,100001,0,2,100002,x'41')",
		"INSERT INTO messages(message_id,sender,writer_id,client_seq,recipient,text) VALUES(2,100001,1,2,100002,x'')",
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatal("SQL integrity violation accepted")
		}
		sameState(t, s, before)
	}
	// A failed Record cannot partially commit, even if a bad callback ignores it.
	exec(t, s, "CREATE TEMP TRIGGER fail_ref BEFORE INSERT ON main.feeds WHEN NEW.account_id=100002 BEGIN SELECT RAISE(ABORT,'injected'); END")
	err := s.Transact(func(tx acceptance.Transaction) error {
		m := acceptance.Message{ID: 2, Key: acceptance.RetryKey{AccountID: alice.AccountID, WriterID: 1, ClientSeq: 2}, Recipient: bob.AccountID, Text: "staged"}
		if err := tx.Record(m, []acceptance.FeedReference{{AccountID: alice.AccountID, Cursor: 2, MessageID: 2}, {AccountID: bob.AccountID, Cursor: 2, MessageID: 2}}); err == nil {
			t.Fatal("injected Record did not fail")
		}
		return nil
	})
	if err == nil {
		t.Fatal("ignored Record failure committed partial state")
	}
	sameState(t, s, before)
	if err := s.Transact(nil); err == nil {
		t.Fatal("nil callback accepted")
	}
}

func TestEscapedFilePathAndUnversionedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "with space #&;.sqlite")
	s := opened(t, path)
	accepted(t, newAcceptor(t, s, nil), alice, 1, bob.AccountID, "escaped path")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, opened(t, path)); got.last != 1 {
		t.Fatal("escaped path reopened another database")
	}
	foreign := filepath.Join(t.TempDir(), "foreign.sqlite")
	db, err := sql.Open("sqlite", foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE unrelated(value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(foreign); !errors.Is(err, ErrSchema) {
		if store != nil {
			store.Close()
		}
		t.Fatal("unversioned foreign database modified")
	}
}
