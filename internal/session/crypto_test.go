package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"relaypulse/internal/store"
)

func TestVaultRoundTripAndTamperRejection(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := NewVault(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	want := Data{UserAgent: "test-agent", Cookies: []Cookie{{Name: "cf_clearance", Value: "secret"}}}
	nonce, ciphertext, err := vault.Encrypt(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := vault.Decrypt(nonce, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserAgent != want.UserAgent || got.Cookies[0].Value != want.Cookies[0].Value {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	ciphertext[0] ^= 1
	if _, err := vault.Decrypt(nonce, ciphertext); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, _, err := vault.Encrypt(Data{Cookies: []Cookie{{Name: "bad;name", Value: "x"}}}); err == nil {
		t.Fatal("invalid cookie name accepted")
	}
}

func TestVaultInfersNewAPIAuthTypeBeforeEncryption(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := NewVault(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	nonce, ciphertext, err := vault.Encrypt(Data{AccessToken: "access", UserID: "42"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := vault.Decrypt(nonce, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.AuthType != AuthTypeNewAPIToken {
		t.Fatalf("auth type = %q, want %q", decoded.AuthType, AuthTypeNewAPIToken)
	}
}

func TestVaultPersistsEncryptedSession(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	vault, _ := NewVault(base64.RawURLEncoding.EncodeToString(key))
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "test", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "newapi-pricing", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	if err := vault.Save(context.Background(), db, site.ID, Data{Cookies: []Cookie{{Name: "sid", Value: "value"}}}, &expires); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := vault.Load(context.Background(), db, site.ID)
	if err != nil || loaded.Cookies[0].Value != "value" {
		t.Fatalf("load session: %+v %v", loaded, err)
	}
	var plaintext string
	if err := db.DB().QueryRow(`SELECT hex(ciphertext) FROM encrypted_sessions WHERE site_id = ?`, site.ID).Scan(&plaintext); err != nil {
		t.Fatal(err)
	}
	if plaintext == "" {
		t.Fatal("ciphertext is empty")
	}
}
