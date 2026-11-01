// Package sqlitestore provides isolated SQLite acceptance and history adapters.
// It does not authenticate callers or emit protocol ACKs.
package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
)

var (
	ErrSchema        = errors.New("unsupported or incompatible SQLite acceptance schema")
	ErrConfiguration = errors.New("required SQLite durability configuration is not active")
	ErrRecord        = errors.New("invalid acceptance record or allocation")
)

const busyTimeoutMS = 5000

type Store struct{ db *sql.DB }

var _ acceptance.Store = (*Store)(nil)

// Open accepts a filesystem path, not a DSN. The parent directory must exist.
// Use a local filesystem with reliable SQLite locking/fsync behavior; do not use
// a network filesystem. In-memory databases and caller-supplied URI options are
// intentionally unsupported. No directory or database is created implicitly here
// beyond SQLite creating the specifically named file and its WAL/SHM sidecars.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" || strings.HasPrefix(strings.ToLower(path), "file:") {
		return nil, errors.New("a file-backed SQLite path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	filename := filepath.ToSlash(abs)
	if strings.HasPrefix(filename, "//") {
		return nil, errors.New("UNC database paths are unsupported")
	}
	if !strings.HasPrefix(filename, "/") {
		filename = "/" + filename
	}
	u := url.URL{Scheme: "file", Path: filename}
	q := url.Values{"mode": {"rwc"}, "_txlock": {"immediate"}, "_pragma": {
		"busy_timeout(5000)", "foreign_keys(ON)", "journal_mode(WAL)", "synchronous(FULL)",
	}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.withTx(initialize); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func verifyConfiguration(q queryRower) error {
	var mode string
	var synchronous, foreignKeys, timeout int
	if err := q.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		return err
	}
	if err := q.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&synchronous); err != nil {
		return err
	}
	if err := q.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return err
	}
	if err := q.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") || synchronous != 2 || foreignKeys != 1 || timeout != busyTimeoutMS {
		return ErrConfiguration
	}
	return nil
}

// _txlock=immediate makes Begin acquire SQLite's writer reservation before ANY
// callback lookup. This serializes writers across pools/instances/processes,
// unlike a Go mutex or a default deferred SQL transaction alone.
func (s *Store) withTx(fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if rollback := tx.Rollback(); rollback != nil && !errors.Is(rollback, sql.ErrTxDone) {
			err = errors.Join(err, rollback)
		}
	}()
	if err = verifyConfiguration(tx); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Transact(fn func(acceptance.Transaction) error) error {
	if fn == nil {
		return errors.New("acceptance transaction callback is required")
	}
	return s.withTx(func(tx *sql.Tx) error {
		t := &transaction{tx: tx}
		if err := fn(t); err != nil {
			return err
		}
		return t.failed
	})
}

type transaction struct {
	tx       *sql.Tx
	recorded bool
	failed   error
}

var _ acceptance.Transaction = (*transaction)(nil)

func (t *transaction) Lookup(key acceptance.RetryKey) (acceptance.Message, bool, error) {
	m := acceptance.Message{Key: key}
	var text []byte
	err := t.tx.QueryRow("SELECT message_id, recipient, text FROM messages WHERE sender=? AND writer_id=? AND client_seq=?", key.AccountID, key.WriterID, key.ClientSeq).Scan(&m.ID, &m.Recipient, &text)
	if errors.Is(err, sql.ErrNoRows) {
		return acceptance.Message{}, false, nil
	}
	if err != nil {
		return acceptance.Message{}, false, err
	}
	m.Text = string(text)
	return m, true, nil
}
func (t *transaction) LastMessageID() (int64, error) {
	var last int64
	err := t.tx.QueryRow("SELECT message_id FROM allocation WHERE singleton=1").Scan(&last)
	return last, err
}
func (t *transaction) LastCursor(account uint32) (int64, error) {
	var last int64
	err := t.tx.QueryRow("SELECT cursor FROM account_cursors WHERE account_id=?", account).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return last, err
}

func (t *transaction) Record(m acceptance.Message, refs []acceptance.FeedReference) (err error) {
	defer func() {
		if err != nil {
			t.failed = err
		}
	}()
	if t.recorded || m.ID <= 0 || !deliveryv1.ValidID(m.Key.AccountID) || !deliveryv1.ValidID(m.Recipient) || m.Key.WriterID <= 0 || m.Key.ClientSeq <= 0 || !deliveryv1.ValidText(m.Text) {
		return ErrRecord
	}
	last, err := t.LastMessageID()
	if err != nil {
		return err
	}
	if last == 1<<63-1 || m.ID != last+1 {
		return ErrRecord
	}
	want := 2
	if m.Key.AccountID == m.Recipient {
		want = 1
	}
	if len(refs) != want {
		return ErrRecord
	}
	seenSender, seenRecipient := false, false
	for _, ref := range refs {
		switch {
		case ref.AccountID == m.Key.AccountID && !seenSender:
			seenSender = true
		case ref.AccountID == m.Recipient && m.Recipient != m.Key.AccountID && !seenRecipient:
			seenRecipient = true
		default:
			return ErrRecord
		}
		last, err := t.LastCursor(ref.AccountID)
		if err != nil {
			return err
		}
		if last == 1<<63-1 || ref.Cursor <= 0 || ref.Cursor != last+1 || ref.MessageID != m.ID {
			return ErrRecord
		}
	}
	if _, err = t.tx.Exec("INSERT INTO messages(message_id,sender,writer_id,client_seq,recipient,text) VALUES(?,?,?,?,?,?)", m.ID, m.Key.AccountID, m.Key.WriterID, m.Key.ClientSeq, m.Recipient, []byte(m.Text)); err != nil {
		return err
	}
	result, err := t.tx.Exec("UPDATE allocation SET message_id=? WHERE singleton=1", m.ID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrRecord
	}
	for _, ref := range refs {
		if _, err = t.tx.Exec("INSERT INTO account_cursors(account_id,cursor) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET cursor=excluded.cursor", ref.AccountID, ref.Cursor); err != nil {
			return err
		}
		if _, err = t.tx.Exec("INSERT INTO feeds(account_id,cursor,message_id) VALUES(?,?,?)", ref.AccountID, ref.Cursor, ref.MessageID); err != nil {
			return err
		}
	}
	t.recorded = true
	return nil
}
