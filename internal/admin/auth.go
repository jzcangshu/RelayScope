package admin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Auth struct {
	mu           sync.Mutex
	passwordHash []byte
	sessions     map[string]time.Time
	csrfTokens   map[string]string
	maxFailures  map[string]failureState
	sessionTTL   time.Duration
}

func (auth *Auth) CSRFToken(sessionToken string) (string, bool) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	now := time.Now()
	auth.cleanupExpiredLocked(now)
	if expires, ok := auth.sessions[sessionToken]; !ok || !expires.After(now) {
		return "", false
	}
	token, ok := auth.csrfTokens[sessionToken]
	return token, ok
}

func (auth *Auth) NewCSRFToken(sessionToken string) (string, bool) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	now := time.Now()
	auth.cleanupExpiredLocked(now)
	expires, ok := auth.sessions[sessionToken]
	if !ok || !expires.After(now) {
		return "", false
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", false
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	auth.csrfTokens[sessionToken] = token
	return token, true
}

type failureState struct {
	count      int
	until      time.Time
	lastFailed time.Time
}

const failureWindow = 10 * time.Minute

func (auth *Auth) cleanupExpiredLocked(now time.Time) {
	for token, expires := range auth.sessions {
		if !expires.After(now) {
			delete(auth.sessions, token)
			delete(auth.csrfTokens, token)
		}
	}
	for remoteAddr, state := range auth.maxFailures {
		if !state.until.After(now) && !state.lastFailed.IsZero() && now.Sub(state.lastFailed) >= failureWindow {
			delete(auth.maxFailures, remoteAddr)
		}
	}
}

func NewAuth(password string) (*Auth, error) {
	if len(password) < 16 {
		return nil, errors.New("administrator password must be at least 16 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash administrator password: %w", err)
	}
	return &Auth{passwordHash: hash, sessions: make(map[string]time.Time), csrfTokens: make(map[string]string), maxFailures: make(map[string]failureState), sessionTTL: 12 * time.Hour}, nil
}

func (auth *Auth) Login(remoteAddr, password string) (string, bool) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	now := time.Now()
	auth.cleanupExpiredLocked(now)
	if state := auth.maxFailures[remoteAddr]; state.until.After(now) {
		return "", false
	}
	if bcrypt.CompareHashAndPassword(auth.passwordHash, []byte(password)) != nil {
		state := auth.maxFailures[remoteAddr]
		state.count++
		state.lastFailed = now
		if state.count >= 5 {
			state.until = now.Add(10 * time.Minute)
			state.count = 0
		}
		auth.maxFailures[remoteAddr] = state
		return "", false
	}
	delete(auth.maxFailures, remoteAddr)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", false
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	auth.sessions[token] = now.Add(auth.sessionTTL)
	return token, true
}

func (auth *Auth) Valid(token string) bool {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	now := time.Now()
	auth.cleanupExpiredLocked(now)
	_, ok := auth.sessions[token]
	if !ok {
		return false
	}
	return true
}

func (auth *Auth) Logout(token string) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	delete(auth.sessions, token)
	delete(auth.csrfTokens, token)
}

func (auth *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie("relayscope_admin")
		if err != nil || !auth.Valid(cookie.Value) {
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			writer.Header().Set("Cache-Control", "no-store")
			writer.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(writer).Encode(map[string]string{"error": "管理员登录已失效"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}
