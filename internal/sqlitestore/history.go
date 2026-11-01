package sqlitestore

import (
	"context"
	"database/sql"
	"errors"

	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
)

var _ historysync.Store = (*Store)(nil)

// View uses the pinned driver's read-only transaction option to select deferred
// BEGIN rather than the writer pool's BEGIN IMMEDIATE. The initial tail SELECT
// pins the committed WAL snapshot before the callback. No additional pool exists.
// ReadOnly selects transaction mode; the read-only Reader API supplies no writes.
// Callers must establish account ownership and must not reenter this store from
// the callback: its single pooled connection is occupied for the view's lifetime.
func (s *Store) View(accountID uint32, fn func(historysync.Reader) error) (err error) {
	if !deliveryv1.ValidID(accountID) || fn == nil {
		return historysync.ErrInvalid
	}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
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
	r := &historyReader{tx: tx, account: accountID}
	err = tx.QueryRow("SELECT cursor FROM main.account_cursors WHERE account_id=?", accountID).Scan(&r.tail)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return err
	}
	if r.tail < 0 {
		return historysync.ErrStoreState
	}
	if err = fn(r); err != nil {
		return err
	}
	return tx.Commit()
}

type historyReader struct {
	tx      *sql.Tx
	account uint32
	tail    int64
}

func (r *historyReader) Tail() (int64, error) { return r.tail, nil }

// LEFT JOIN also makes an externally corrupted dangling feed fail Scan instead
// of silently omitting it. Normal adapter writes enforce the foreign key.
const historyQuery = `SELECT f.account_id, f.cursor, f.message_id,
 m.message_id, m.sender, m.writer_id, m.client_seq, m.recipient, m.text
 FROM main.feeds AS f LEFT JOIN main.messages AS m ON m.message_id=f.message_id
 WHERE f.account_id=? AND f.cursor>? AND f.cursor<=?
 ORDER BY f.cursor LIMIT ?`

func (r *historyReader) Records(after, through int64, max int) (records []historysync.Record, err error) {
	if max < 1 || max > 9 {
		return nil, historysync.ErrInvalid
	}
	if after < 0 || through < after || through > r.tail {
		return nil, historysync.ErrCursorInvalid
	}
	rows, err := r.tx.Query(historyQuery, r.account, after, through, max)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			records = nil
			err = errors.Join(err, closeErr)
		}
	}()
	records = make([]historysync.Record, 0, max)
	for rows.Next() {
		var record historysync.Record
		var text []byte
		err = rows.Scan(&record.Reference.AccountID, &record.Reference.Cursor, &record.Reference.MessageID,
			&record.Message.ID, &record.Message.Key.AccountID, &record.Message.Key.WriterID,
			&record.Message.Key.ClientSeq, &record.Message.Recipient, &text)
		if err != nil {
			return nil, err
		}
		record.Message.Text = string(text)
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
