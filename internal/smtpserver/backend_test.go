package smtpserver

import (
	"crypto/ed25519"
	"io"
	"log"
	"net"
	"testing"

	"github.com/emersion/go-smtp"
	"github.com/neilalexander/yggmail/internal/config"
	"github.com/neilalexander/yggmail/internal/storage/sqlite3"
	"golang.org/x/crypto/bcrypt"
)

func TestAuthenticateLoginAttachesSMTPSession(t *testing.T) {
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

	state := &smtp.ConnectionState{
		RemoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234},
	}
	backend := &Backend{
		Mode:    BackendModeInternal,
		Config:  &config.Config{PublicKey: make(ed25519.PublicKey, ed25519.PublicKeySize)},
		Log:     log.New(io.Discard, "", 0),
		Storage: store,
	}
	var session smtp.Session
	if err := AuthenticateLogin(
		backend, state, func(got smtp.Session) { session = got }, "user", "password",
	); err != nil {
		t.Fatal(err)
	}
	if session == nil {
		t.Fatal("authenticated session was not attached to connection")
	}
	local, ok := session.(*SessionLocal)
	if !ok {
		t.Fatalf("session type = %T, want *SessionLocal", session)
	}
	if local.state != state {
		t.Fatal("session did not retain its connection state")
	}
}
