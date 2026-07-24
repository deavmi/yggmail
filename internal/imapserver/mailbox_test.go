package imapserver

import (
	"io"
	"slices"
	"testing"

	"github.com/emersion/go-imap"
	"github.com/neilalexander/yggmail/internal/storage/sqlite3"
)

func testMailbox(t *testing.T) (*Mailbox, *sqlite3.SQLite3Storage) {
	t.Helper()

	store, err := sqlite3.NewSQLite3StorageStorage(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := store.MailboxCreate("INBOX"); err != nil {
		t.Fatal(err)
	}

	return &Mailbox{
		backend: &Backend{Storage: store},
		name:    "INBOX",
	}, store
}

func TestListMessagesUsesMailboxSequenceOrder(t *testing.T) {
	mailbox, store := testMailbox(t)
	for range 3 {
		if _, err := store.MailCreate("INBOX", []byte("Subject: test\r\n\r\nbody")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MailDelete("INBOX", 2); err != nil {
		t.Fatal(err)
	}
	if err := store.MailExpunge("INBOX"); err != nil {
		t.Fatal(err)
	}

	seqSet, err := imap.ParseSeqSet("1:*")
	if err != nil {
		t.Fatal(err)
	}
	messages := make(chan *imap.Message, 2)
	if err := mailbox.ListMessages(false, seqSet, []imap.FetchItem{imap.FetchUid}, messages); err != nil {
		t.Fatal(err)
	}

	var gotSeqs, gotUIDs []uint32
	for message := range messages {
		gotSeqs = append(gotSeqs, message.SeqNum)
		gotUIDs = append(gotUIDs, message.Uid)
	}
	if !slices.Equal(gotSeqs, []uint32{1, 2}) {
		t.Fatalf("sequence numbers = %v, want [1 2]", gotSeqs)
	}
	if !slices.Equal(gotUIDs, []uint32{1, 3}) {
		t.Fatalf("UIDs = %v, want [1 3]", gotUIDs)
	}
}

func TestSearchMessagesFiltersUIDAndUnseen(t *testing.T) {
	mailbox, store := testMailbox(t)
	for range 3 {
		if _, err := store.MailCreate("INBOX", []byte("Subject: test\r\n\r\nbody")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MailDelete("INBOX", 2); err != nil {
		t.Fatal(err)
	}
	if err := store.MailExpunge("INBOX"); err != nil {
		t.Fatal(err)
	}
	if err := store.MailUpdateFlags("INBOX", 3, true, false, false, false); err != nil {
		t.Fatal(err)
	}

	uidSet, err := imap.ParseSeqSet("1:*")
	if err != nil {
		t.Fatal(err)
	}
	criteria := imap.NewSearchCriteria()
	criteria.Uid = uidSet
	criteria.WithoutFlags = []string{imap.SeenFlag}

	uids, err := mailbox.SearchMessages(true, criteria)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(uids, []uint32{1}) {
		t.Fatalf("UID SEARCH results = %v, want [1]", uids)
	}

	criteria = imap.NewSearchCriteria()
	criteria.Uid, err = imap.ParseSeqSet("3")
	if err != nil {
		t.Fatal(err)
	}
	seqNums, err := mailbox.SearchMessages(false, criteria)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seqNums, []uint32{2}) {
		t.Fatalf("SEARCH results = %v, want sequence number [2]", seqNums)
	}
}

func TestListMessagesFetchesBody(t *testing.T) {
	mailbox, store := testMailbox(t)
	if _, err := store.MailCreate("INBOX", []byte("Subject: test\r\n\r\nbody")); err != nil {
		t.Fatal(err)
	}

	seqSet, err := imap.ParseSeqSet("*")
	if err != nil {
		t.Fatal(err)
	}
	messages := make(chan *imap.Message, 1)
	section := &imap.BodySectionName{}
	if err := mailbox.ListMessages(false, seqSet, []imap.FetchItem{section.FetchItem()}, messages); err != nil {
		t.Fatal(err)
	}
	message := <-messages
	body, err := io.ReadAll(message.GetBody(section))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "Subject: test\r\n\r\nbody" {
		t.Fatalf("body = %q", body)
	}
}

func TestMoveFromOutboxCancelsDeliveryAndAllocatesDestinationID(t *testing.T) {
	mailbox, store := testMailbox(t)
	mailbox.name = "Outbox"
	for _, name := range []string{"Outbox", "Archive"} {
		if err := store.MailboxCreate(name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.MailCreate("Archive", []byte("existing")); err != nil {
		t.Fatal(err)
	}
	id, err := store.MailCreate("Outbox", []byte("outgoing"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueInsertDestinationForID("peer", id, "from", "to"); err != nil {
		t.Fatal(err)
	}

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uint32(id))
	if err := mailbox.MoveMessages(true, seqSet, "Archive"); err != nil {
		t.Fatal(err)
	}

	if pending, err := store.QueueSelectIsMessagePendingSend("Outbox", id); err != nil || pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
	_, moved, err := store.MailSelect("Archive", 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(moved.Mail) != "outgoing" {
		t.Fatalf("moved body = %q", moved.Mail)
	}
}
