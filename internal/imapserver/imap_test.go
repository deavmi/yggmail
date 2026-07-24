package imapserver

import (
	"crypto/ed25519"
	"io"
	"log"
	"net"
	"testing"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/server"
	"github.com/neilalexander/yggmail/internal/config"
	"github.com/neilalexander/yggmail/internal/storage/sqlite3"
	"golang.org/x/crypto/bcrypt"
)

func TestAuthenticateLoginAttachesIMAPUser(t *testing.T) {
	store, err := sqlite3.NewSQLite3StorageStorage(t.TempDir() + "/mail.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() // nolint:errcheck
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigSetPassword(string(hash)); err != nil {
		t.Fatal(err)
	}

	context := &server.Context{State: imap.NotAuthenticatedState}
	info := &imap.ConnInfo{
		RemoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234},
	}
	backend := &Backend{
		Config:  &config.Config{PublicKey: make(ed25519.PublicKey, ed25519.PublicKeySize)},
		Log:     log.New(io.Discard, "", 0),
		Storage: store,
	}
	if err := authenticateLogin(backend, info, context, "user", "password"); err != nil {
		t.Fatal(err)
	}
	if context.State != imap.AuthenticatedState {
		t.Fatalf("state = %v, want authenticated", context.State)
	}
	if context.User == nil {
		t.Fatal("authenticated user was not attached to context")
	}
}
