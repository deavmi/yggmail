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

func TestQueueMarkDeliveredMovesToNextSentID(t *testing.T) {
	store, err := NewSQLite3StorageStorage(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() // nolint:errcheck
	for _, mailbox := range []string{"Outbox", "Sent"} {
		if err := store.MailboxCreate(mailbox); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.MailCreate("Sent", []byte("existing")); err != nil {
		t.Fatal(err)
	}
	outboxID, err := store.MailCreate("Outbox", []byte("outgoing"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MailUpdateFlags("Outbox", outboxID, true, true, true, false); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueInsertDestinationForID("peer", outboxID, "from", "to"); err != nil {
		t.Fatal(err)
	}

	if err := store.QueueMarkDelivered("peer", outboxID); err != nil {
		t.Fatal(err)
	}
	if count, err := store.MailCount("Outbox"); err != nil || count != 0 {
		t.Fatalf("Outbox count = %d, err = %v", count, err)
	}
	_, sent, err := store.MailSelect("Sent", 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(sent.Mail) != "outgoing" || !sent.Seen || !sent.Answered || !sent.Flagged {
		t.Fatalf("moved mail = %+v", sent)
	}
	if pending, err := store.QueueSelectIsMessagePendingSend("Outbox", outboxID); err != nil || pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
}

func TestQueueMarkDeliveredRollsBackMissingSentMailbox(t *testing.T) {
	store, err := NewSQLite3StorageStorage(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() // nolint:errcheck
	if err := store.MailboxCreate("Outbox"); err != nil {
		t.Fatal(err)
	}
	id, err := store.MailCreate("Outbox", []byte("outgoing"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueInsertDestinationForID("peer", id, "from", "to"); err != nil {
		t.Fatal(err)
	}

	if err := store.QueueMarkDelivered("peer", id); err == nil {
		t.Fatal("QueueMarkDelivered succeeded without Sent mailbox")
	}
	if pending, err := store.QueueSelectIsMessagePendingSend("Outbox", id); err != nil || !pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
	if _, _, err := store.MailSelect("Outbox", id); err != nil {
		t.Fatalf("Outbox mail was lost: %v", err)
	}
}
