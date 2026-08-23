package admin

import (
	"crypto/rand"
	"encoding/base64"
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
	maxFailures  map[string]failureState
	sessionTTL   time.Duration
}

func (auth *Auth) CSRFToken(sessionToken string) (string, bool) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if expires, ok := auth.sessions[sessionToken]; !ok || expires.Before(time.Now()) {
		return "", false
	}
	return sessionToken, true
}

type failureState struct {
	count int
	until time.Time
}

func NewAuth(password string) (*Auth, error) {
	if len(password) < 16 {
		return nil, errors.New("administrator password must be at least 16 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash administrator password: %w", err)
	}
	return &Auth{passwordHash: hash, sessions: make(map[string]time.Time), maxFailures: make(map[string]failureState), sessionTTL: 12 * time.Hour}, nil
}

func (auth *Auth) Login(remoteAddr, password string) (string, bool) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	now := time.Now()
	if state := auth.maxFailures[remoteAddr]; state.until.After(now) {
		return "", false
	}
	if bcrypt.CompareHashAndPassword(auth.passwordHash, []byte(password)) != nil {
		state := auth.maxFailures[remoteAddr]
		state.count++
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

func (auth *Auth) VerifyPassword(password string) bool {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	return bcrypt.CompareHashAndPassword(auth.passwordHash, []byte(password)) == nil
}

func (auth *Auth) Valid(token string) bool {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	expires, ok := auth.sessions[token]
	if !ok {
		return false
	}
	if expires.Before(time.Now()) {
		delete(auth.sessions, token)
		return false
	}
	return true
}

func (auth *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie("relaypulse_admin")
		if err != nil || !auth.Valid(cookie.Value) {
			http.Error(writer, "管理员登录已失效", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
