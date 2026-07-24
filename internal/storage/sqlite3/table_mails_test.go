package sqlite3

import (
	"database/sql"
	"slices"
	"strings"
	"testing"
)

func TestMailsSeenIndexIsAddedToExistingSchema(t *testing.T) {
	filename := t.TempDir() + "/mail.db"
	legacyDB, err := sql.Open("sqlite3", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(mailboxesSchema); err != nil {
		t.Fatal(err)
	}
	legacyMailsSchema, _, found := strings.Cut(mailsSchema, "CREATE INDEX")
	if !found {
		t.Fatal("mailsSchema has no additive index statement")
	}
	if _, err := legacyDB.Exec(legacyMailsSchema); err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLite3StorageStorage(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()

	rows, err := store.db.Query("PRAGMA index_list(mails)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() // nolint:errcheck

	var indexes []string
	for rows.Next() {
		var sequence int
		var name, origin string
		var unique, partial bool
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(indexes, "mails_mailbox_seen_id") {
		t.Fatalf("mails indexes = %v, missing mails_mailbox_seen_id", indexes)
	}

	plan, err := store.db.Query(
		"EXPLAIN QUERY PLAN "+selectMailsSeenStmt,
		"INBOX", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close() // nolint:errcheck
	var usedSeenIndex bool
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		usedSeenIndex = usedSeenIndex ||
			detail == "SEARCH mails USING INDEX mails_mailbox_seen_id (mailbox=? AND seen=?)"
	}
	if err := plan.Err(); err != nil {
		t.Fatal(err)
	}
	if !usedSeenIndex {
		t.Fatal("seen-filtered mailbox SELECT does not use mails_mailbox_seen_id")
	}
}

func TestMailListUsesUIDOrder(t *testing.T) {
	store, err := NewSQLite3StorageStorage(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := store.MailboxCreate("INBOX"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := store.MailCreate("INBOX", []byte("mail")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MailDelete("INBOX", 2); err != nil {
		t.Fatal(err)
	}
	if err := store.MailExpunge("INBOX"); err != nil {
		t.Fatal(err)
	}

	mails, err := store.MailList("INBOX", nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, mail := range mails {
		ids = append(ids, mail.ID)
	}
	if !slices.Equal(ids, []int{1, 3}) {
		t.Fatalf("mail IDs = %v, want [1 3]", ids)
	}
}
