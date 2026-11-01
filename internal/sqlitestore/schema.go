package sqlitestore

import (
	"database/sql"
	"strings"
)

const schemaVersion = 1

// The unique retry tuple is the permanent association to this message's original
// recipient and bytes. A separate retry table would duplicate the same data.
var schema = []struct{ name, ddl string }{
	{"allocation", `CREATE TABLE allocation (
 singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
 message_id INTEGER NOT NULL CHECK(message_id >= 0)
) STRICT`},
	{"account_cursors", `CREATE TABLE account_cursors (
 account_id INTEGER PRIMARY KEY CHECK(account_id BETWEEN 100000 AND 999999999),
 cursor INTEGER NOT NULL CHECK(cursor >= 0)
) STRICT`},
	{"messages", `CREATE TABLE messages (
 message_id INTEGER PRIMARY KEY CHECK(message_id > 0),
 sender INTEGER NOT NULL CHECK(sender BETWEEN 100000 AND 999999999),
 writer_id INTEGER NOT NULL CHECK(writer_id > 0),
 client_seq INTEGER NOT NULL CHECK(client_seq > 0),
 recipient INTEGER NOT NULL CHECK(recipient BETWEEN 100000 AND 999999999),
 text BLOB NOT NULL CHECK(length(text) BETWEEN 1 AND 2000),
 UNIQUE(sender, writer_id, client_seq)
) STRICT`},
	{"feeds", `CREATE TABLE feeds (
 account_id INTEGER NOT NULL REFERENCES account_cursors(account_id),
 cursor INTEGER NOT NULL CHECK(cursor > 0),
 message_id INTEGER NOT NULL REFERENCES messages(message_id),
 PRIMARY KEY(account_id, cursor),
 UNIQUE(account_id, message_id)
) STRICT`},
}

func initialize(tx *sql.Tx) error {
	var version, objects int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if err := tx.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&objects); err != nil {
		return err
	}
	switch version {
	case 0:
		if objects != 0 {
			return ErrSchema
		}
		for _, table := range schema {
			if _, err := tx.Exec(table.ddl); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("INSERT INTO allocation(singleton, message_id) VALUES(1, 0)"); err != nil {
			return err
		}
		_, err := tx.Exec("PRAGMA user_version = 1")
		return err
	case schemaVersion:
		if objects != len(schema) {
			return ErrSchema
		}
		for _, table := range schema {
			var actual string
			if err := tx.QueryRow("SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", table.name).Scan(&actual); err != nil {
				return ErrSchema
			}
			if strings.Join(strings.Fields(actual), " ") != strings.Join(strings.Fields(table.ddl), " ") {
				return ErrSchema
			}
		}
		var last int64
		if err := tx.QueryRow("SELECT message_id FROM allocation WHERE singleton=1").Scan(&last); err != nil {
			return ErrSchema
		}
		return nil
	default:
		return ErrSchema
	}
}
