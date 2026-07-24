package imapserver

import (
	"io"
	"log"
	"slices"
	"testing"

	"github.com/emersion/go-imap"
)

func TestInboxNameIsCaseInsensitive(t *testing.T) {
	inbox, store := testMailbox(t)
	user := &User{
		backend: inbox.backend,
		log:     log.New(io.Discard, "", 0),
	}

	for _, name := range []string{"INBOX", "inbox", "Inbox", "iNbOx"} {
		mailbox, err := user.GetMailbox(name)
		if err != nil {
			t.Fatalf("GetMailbox(%q): %v", name, err)
		}
		if mailbox.Name() != "INBOX" {
			t.Fatalf("GetMailbox(%q).Name() = %q, want INBOX", name, mailbox.Name())
		}
	}

	if err := user.CreateMailbox("inbox"); err != nil {
		t.Fatal(err)
	}
	names, err := store.MailboxList(false)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, "inbox") {
		t.Fatalf("mailboxes contain a separate lowercase INBOX: %v", names)
	}
	if err := user.DeleteMailbox("inbox"); err == nil {
		t.Fatal("DeleteMailbox accepted a lowercase INBOX")
	}
	if err := user.RenameMailbox("Inbox", "Archive"); err == nil {
		t.Fatal("RenameMailbox accepted a mixed-case INBOX")
	}
}

func TestCopyAndMoveCanonicalizeInboxDestination(t *testing.T) {
	inbox, store := testMailbox(t)
	if err := store.MailboxCreate("Archive"); err != nil {
		t.Fatal(err)
	}
	source := &Mailbox{backend: inbox.backend, name: "Archive"}
	id, err := store.MailCreate("Archive", []byte("mail"))
	if err != nil {
		t.Fatal(err)
	}
	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uint32(id))

	if err := source.CopyMessages(true, seqSet, "inbox"); err != nil {
		t.Fatal(err)
	}
	if count, err := store.MailCount("INBOX"); err != nil || count != 1 {
		t.Fatalf("INBOX count after copy = %d, err = %v, want 1", count, err)
	}
	if err := source.MoveMessages(true, seqSet, "Inbox"); err != nil {
		t.Fatal(err)
	}
	if count, err := store.MailCount("INBOX"); err != nil || count != 2 {
		t.Fatalf("INBOX count after move = %d, err = %v, want 2", count, err)
	}
}
