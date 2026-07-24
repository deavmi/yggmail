package smtpsender

import (
	"bufio"
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/neilalexander/yggmail/internal/config"
	"github.com/neilalexander/yggmail/internal/storage"
	"github.com/neilalexander/yggmail/internal/storage/sqlite3"
)

type testTransport struct {
	acceptData bool
}

func (t *testTransport) Listener() net.Listener {
	return nil
}

func (t *testTransport) Dial(string) (net.Conn, error) {
	client, server := net.Pipe()
	go serveSMTP(server, t.acceptData)
	return client, nil
}

func serveSMTP(conn net.Conn, acceptData bool) {
	defer conn.Close() // nolint:errcheck
	reader := bufio.NewReader(conn)
	_, _ = io.WriteString(conn, "220 test ESMTP\r\n")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO "):
			_, _ = io.WriteString(conn, "250 hello\r\n")
		case strings.HasPrefix(line, "MAIL FROM:"):
			_, _ = io.WriteString(conn, "250 sender ok\r\n")
		case strings.HasPrefix(line, "RCPT TO:"):
			_, _ = io.WriteString(conn, "250 recipient ok\r\n")
		case strings.HasPrefix(line, "DATA"):
			_, _ = io.WriteString(conn, "354 continue\r\n")
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
			}
			if acceptData {
				_, _ = io.WriteString(conn, "250 queued\r\n")
			} else {
				_, _ = io.WriteString(conn, "550 rejected\r\n")
			}
		default:
			_, _ = io.WriteString(conn, "250 ok\r\n")
		}
	}
}

type recordingStorage struct {
	storage.Storage
	delivered atomic.Bool
}

func (s *recordingStorage) QueueMarkDelivered(destination string, id int) error {
	s.delivered.Store(true)
	return s.Storage.QueueMarkDelivered(destination, id)
}

func TestRejectedDataRemainsQueued(t *testing.T) {
	store, err := sqlite3.NewSQLite3StorageStorage(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() // nolint:errcheck
	for _, mailbox := range []string{"Outbox", "Sent"} {
		if err := store.MailboxCreate(mailbox); err != nil {
			t.Fatal(err)
		}
	}
	id, err := store.MailCreate("Outbox", []byte("Subject: test\r\n\r\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueInsertDestinationForID(
		"peer", id, "sender@yggmail", "recipient@yggmail",
	); err != nil {
		t.Fatal(err)
	}

	recording := &recordingStorage{Storage: store}
	queue := &Queue{
		queues: &Queues{
			Config:    &config.Config{PublicKey: make(ed25519.PublicKey, ed25519.PublicKeySize)},
			Log:       log.New(io.Discard, "", 0),
			Transport: &testTransport{acceptData: false},
			Storage:   recording,
		},
		destination: "peer",
	}
	queue.run()

	if recording.delivered.Load() {
		t.Fatal("delivery was finalized after remote DATA rejection")
	}
	if pending, err := store.QueueSelectIsMessagePendingSend("Outbox", id); err != nil || !pending {
		t.Fatalf("pending = %v, err = %v", pending, err)
	}
	if count, err := store.MailCount("Outbox"); err != nil || count != 1 {
		t.Fatalf("Outbox count = %d, err = %v", count, err)
	}
}

func TestSelfAddressedMailGoesToInboxAndSent(t *testing.T) {
	store, err := sqlite3.NewSQLite3StorageStorage(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() // nolint:errcheck
	for _, mailbox := range []string{"INBOX", "Outbox", "Sent"} {
		if err := store.MailboxCreate(mailbox); err != nil {
			t.Fatal(err)
		}
	}
	publicKey := make(ed25519.PublicKey, ed25519.PublicKeySize)
	address := hex.EncodeToString(publicKey) + "@yggmail"
	queues := &Queues{
		Config:  &config.Config{PublicKey: publicKey},
		Log:     log.New(io.Discard, "", 0),
		Storage: store,
	}

	if err := queues.QueueFor(address, []string{address}, []byte("self mail")); err != nil {
		t.Fatal(err)
	}
	for mailbox, want := range map[string]int{"INBOX": 1, "Sent": 1, "Outbox": 0} {
		if count, err := store.MailCount(mailbox); err != nil || count != want {
			t.Fatalf("%s count = %d, err = %v, want %d", mailbox, count, err, want)
		}
	}
}
