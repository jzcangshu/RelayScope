package session

import (
	"testing"
	"time"
)

const testExtensionOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"

func TestSyncManagerPairingIsSingleUseAndOriginBound(t *testing.T) {
	now := time.Date(2026, time.August, 13, 8, 0, 0, 0, time.UTC)
	manager := NewSyncManager(func() time.Time { return now })
	code, expiresAt, err := manager.CreatePairing()
	if err != nil || code == "" || expiresAt != now.Add(PairingTTL) {
		t.Fatalf("create pairing: %q %v %v", code, expiresAt, err)
	}
	token, tokenExpiry, err := manager.Exchange(code, testExtensionOrigin)
	if err != nil || token == "" || tokenExpiry != now.Add(SyncTTL) {
		t.Fatalf("exchange: %q %v %v", token, tokenExpiry, err)
	}
	if _, _, err := manager.Exchange(code, testExtensionOrigin); err == nil {
		t.Fatal("pairing code was reusable")
	}
	if manager.Authorize(token, "chrome-extension://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Fatal("token accepted for another extension")
	}
	if !manager.Authorize(token, testExtensionOrigin) || !manager.Claim(token, testExtensionOrigin) || manager.Authorize(token, testExtensionOrigin) {
		t.Fatal("token claim semantics are invalid")
	}
	if manager.Claim(token, testExtensionOrigin) {
		t.Fatal("claimed token accepted concurrently")
	}
	manager.Finish(token, testExtensionOrigin, false)
	if !manager.Authorize(token, testExtensionOrigin) || !manager.Claim(token, testExtensionOrigin) {
		t.Fatal("failed batch did not release token")
	}
	manager.Finish(token, testExtensionOrigin, true)
	if manager.Authorize(token, testExtensionOrigin) {
		t.Fatal("successful batch did not consume token")
	}
}

func TestSyncManagerExpiresPairingAndToken(t *testing.T) {
	now := time.Date(2026, time.August, 13, 8, 0, 0, 0, time.UTC)
	manager := NewSyncManager(func() time.Time { return now })
	code, _, _ := manager.CreatePairing()
	now = now.Add(PairingTTL + time.Second)
	if _, _, err := manager.Exchange(code, testExtensionOrigin); err == nil {
		t.Fatal("expired pairing code accepted")
	}
	code, _, _ = manager.CreatePairing()
	token, _, _ := manager.Exchange(code, testExtensionOrigin)
	now = now.Add(SyncTTL + time.Second)
	if manager.Authorize(token, testExtensionOrigin) {
		t.Fatal("expired sync token accepted")
	}
}

func TestSyncManagerRateLimitsInvalidPairingAttempts(t *testing.T) {
	now := time.Date(2026, time.August, 13, 8, 0, 0, 0, time.UTC)
	manager := NewSyncManager(func() time.Time { return now })
	for attempt := 0; attempt < maxPairingAttempts; attempt++ {
		if _, _, err := manager.Exchange("invalid", testExtensionOrigin); err == nil {
			t.Fatal("invalid pairing code accepted")
		}
	}
	if _, _, err := manager.Exchange("invalid", testExtensionOrigin); err != ErrPairingRateLimited {
		t.Fatalf("rate limit error = %v", err)
	}
	code, _, err := manager.CreatePairing()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Exchange(code, testExtensionOrigin); err != nil {
		t.Fatalf("valid pairing was blocked by invalid attempts: %v", err)
	}
	now = now.Add(pairingAttemptWindow)
	if _, _, err := manager.Exchange("invalid", testExtensionOrigin); err == ErrPairingRateLimited {
		t.Fatal("pairing rate limit did not reset")
	}
}

func TestNormalizeOrigin(t *testing.T) {
	got, err := NormalizeOrigin("https://example.test/pricing?q=1")
	if err != nil || got != "https://example.test" {
		t.Fatalf("normalize origin = %q %v", got, err)
	}
	for _, value := range []string{"file:///tmp/a", "https://user@example.test", "not a url"} {
		if _, err := NormalizeOrigin(value); err == nil {
			t.Fatalf("accepted invalid origin %q", value)
		}
	}
}
