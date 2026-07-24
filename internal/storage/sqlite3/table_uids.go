package sqlite3

import (
	"database/sql"
	"fmt"
	"time"
)

const mailboxUIDsSchema = `
	CREATE TABLE IF NOT EXISTS mailbox_uids (
		uidvalidity INTEGER PRIMARY KEY AUTOINCREMENT,
		mailbox     TEXT NOT NULL UNIQUE,
		uidnext     INTEGER NOT NULL DEFAULT 1,
		FOREIGN KEY (mailbox) REFERENCES mailboxes(mailbox) ON DELETE CASCADE ON UPDATE CASCADE
	);
`

func initializeMailboxUIDs(db *sql.DB) error {
	if _, err := db.Exec(mailboxUIDsSchema); err != nil {
		return fmt.Errorf("create mailbox UID state: %w", err)
	}

	// Existing clients have previously seen UIDVALIDITY=1. Seed SQLite's
	// AUTOINCREMENT high-water mark above the current Unix time so every
	// migrated mailbox receives a new validity value and invalidates old UIDs.
	now := time.Now().Unix()
	result, err := db.Exec(
		"UPDATE sqlite_sequence SET seq = MAX(seq, $1) WHERE name = 'mailbox_uids'",
		now,
	)
	if err != nil {
		return fmt.Errorf("seed UIDVALIDITY sequence: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect UIDVALIDITY sequence: %w", err)
	}
	if updated == 0 {
		if _, err := db.Exec(
			"INSERT INTO sqlite_sequence (name, seq) VALUES ('mailbox_uids', $1)",
			now,
		); err != nil {
			return fmt.Errorf("initialize UIDVALIDITY sequence: %w", err)
		}
	}

	if _, err := db.Exec(`
		INSERT OR IGNORE INTO mailbox_uids (mailbox, uidnext)
		SELECT mailboxes.mailbox, IFNULL(MAX(mails.id)+1, 1)
		FROM mailboxes
		LEFT JOIN mails ON mails.mailbox = mailboxes.mailbox
		GROUP BY mailboxes.mailbox
	`); err != nil {
		return fmt.Errorf("initialize mailbox UID state: %w", err)
	}
	return nil
}

func ensureMailboxUIDStateTx(txn *sql.Tx, mailbox string) (int64, uint32, error) {
	var (
		next     int64
		validity uint32
	)
	err := txn.QueryRow(`
		SELECT uidnext, uidvalidity FROM mailbox_uids WHERE mailbox = $1
	`, mailbox).Scan(&next, &validity)
	if err == nil {
		return next, validity, nil
	}
	if err != sql.ErrNoRows {
		return 0, 0, err
	}

	if err := txn.QueryRow(
		"SELECT IFNULL(MAX(id)+1, 1) FROM mails WHERE mailbox = $1",
		mailbox,
	).Scan(&next); err != nil {
		return 0, 0, err
	}
	if err := txn.QueryRow(`
		INSERT INTO mailbox_uids (mailbox, uidnext) VALUES ($1, $2)
		RETURNING uidvalidity
	`, mailbox, next).Scan(&validity); err != nil {
		return 0, 0, err
	}
	return next, validity, nil
}

func allocateMailboxUIDTx(txn *sql.Tx, mailbox string) (int, error) {
	next, _, err := ensureMailboxUIDStateTx(txn, mailbox)
	if err != nil {
		return 0, err
	}
	if next <= 0 || next > int64(^uint32(0)) {
		return 0, fmt.Errorf("UID allocation exhausted for mailbox %q", mailbox)
	}
	if _, err := txn.Exec(
		"UPDATE mailbox_uids SET uidnext = $1 WHERE mailbox = $2",
		next+1, mailbox,
	); err != nil {
		return 0, err
	}
	return int(next), nil
}
