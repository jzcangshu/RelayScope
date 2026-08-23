package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	PairingTTL           = 10 * time.Minute
	SyncTTL              = 30 * time.Minute
	pairingAttemptWindow = time.Minute
	maxPairingAttempts   = 10
)

var ErrPairingRateLimited = errors.New("pairing attempts rate limited")

type SyncManager struct {
	mu       sync.Mutex
	now      func() time.Time
	pairings map[[32]byte]time.Time
	tokens   map[[32]byte]syncGrant
	attempts attemptWindow
}

type syncGrant struct {
	origin    string
	expiresAt time.Time
	claimed   bool
}

type attemptWindow struct {
	count     int
	startedAt time.Time
}

func NewSyncManager(now func() time.Time) *SyncManager {
	if now == nil {
		now = time.Now
	}
	return &SyncManager{
		now: now, pairings: make(map[[32]byte]time.Time), tokens: make(map[[32]byte]syncGrant),
	}
}

func (manager *SyncManager) CreatePairing() (string, time.Time, error) {
	code, err := randomToken(12)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := manager.now().UTC().Add(PairingTTL)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.cleanupLocked()
	manager.pairings[sha256.Sum256([]byte(code))] = expiresAt
	return code, expiresAt, nil
}

// CreateToken issues a browser-sync token after the caller has authenticated
// through the administrator login flow. It avoids a second pairing ceremony
// for the single-user extension while retaining origin scoping and expiry.
func (manager *SyncManager) CreateToken(extensionOrigin string) (string, time.Time, error) {
	if !ValidExtensionOrigin(extensionOrigin) {
		return "", time.Time{}, errors.New("invalid extension origin")
	}
	token, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := manager.now().UTC().Add(SyncTTL)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.cleanupLocked()
	manager.tokens[sha256.Sum256([]byte(token))] = syncGrant{origin: extensionOrigin, expiresAt: expiresAt}
	return token, expiresAt, nil
}

func (manager *SyncManager) Exchange(code, extensionOrigin string) (string, time.Time, error) {
	if !ValidExtensionOrigin(extensionOrigin) {
		return "", time.Time{}, errors.New("invalid extension origin")
	}
	key := sha256.Sum256([]byte(strings.TrimSpace(code)))
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.cleanupLocked()
	now := manager.now().UTC()
	expiresAt, ok := manager.pairings[key]
	if ok && expiresAt.After(now) {
		manager.attempts = attemptWindow{}
		delete(manager.pairings, key)
		token, err := randomToken(32)
		if err != nil {
			return "", time.Time{}, err
		}
		tokenExpiry := manager.now().UTC().Add(SyncTTL)
		manager.tokens[sha256.Sum256([]byte(token))] = syncGrant{origin: extensionOrigin, expiresAt: tokenExpiry}
		return token, tokenExpiry, nil
	}
	window := manager.attempts
	if window.startedAt.IsZero() || !now.Before(window.startedAt.Add(pairingAttemptWindow)) {
		window = attemptWindow{startedAt: now}
	}
	if window.count >= maxPairingAttempts {
		return "", time.Time{}, ErrPairingRateLimited
	}
	window.count++
	manager.attempts = window
	return "", time.Time{}, errors.New("invalid or expired pairing code")
}

func (manager *SyncManager) Authorize(token, extensionOrigin string) bool {
	if !ValidExtensionOrigin(extensionOrigin) {
		return false
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.cleanupLocked()
	grant, ok := manager.tokens[sha256.Sum256([]byte(strings.TrimSpace(token)))]
	return ok && !grant.claimed && grant.origin == extensionOrigin && grant.expiresAt.After(manager.now().UTC())
}

func (manager *SyncManager) Claim(token, extensionOrigin string) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.cleanupLocked()
	key := sha256.Sum256([]byte(strings.TrimSpace(token)))
	grant, ok := manager.tokens[key]
	if !ok || grant.claimed || grant.origin != extensionOrigin || !grant.expiresAt.After(manager.now().UTC()) {
		return false
	}
	grant.claimed = true
	manager.tokens[key] = grant
	return true
}

func (manager *SyncManager) Finish(token, extensionOrigin string, succeeded bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	key := sha256.Sum256([]byte(strings.TrimSpace(token)))
	grant, ok := manager.tokens[key]
	if !ok || !grant.claimed || grant.origin != extensionOrigin {
		return
	}
	if succeeded {
		delete(manager.tokens, key)
		return
	}
	grant.claimed = false
	manager.tokens[key] = grant
}

func (manager *SyncManager) cleanupLocked() {
	now := manager.now().UTC()
	for key, expiresAt := range manager.pairings {
		if !expiresAt.After(now) {
			delete(manager.pairings, key)
		}
	}
	for key, grant := range manager.tokens {
		if !grant.expiresAt.After(now) {
			delete(manager.tokens, key)
		}
	}
	if !manager.attempts.startedAt.IsZero() && !now.Before(manager.attempts.startedAt.Add(pairingAttemptWindow)) {
		manager.attempts = attemptWindow{}
	}
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func ValidExtensionOrigin(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "chrome-extension" || parsed.Hostname() == "" || parsed.Port() != "" || parsed.Path != "" {
		return false
	}
	id := parsed.Hostname()
	if len(id) != 32 {
		return false
	}
	for _, char := range id {
		if char < 'a' || char > 'p' {
			return false
		}
	}
	return true
}

func NormalizeOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return "", errors.New("invalid site origin")
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String(), nil
}
