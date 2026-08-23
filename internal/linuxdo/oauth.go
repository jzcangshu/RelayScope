package linuxdo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"relaypulse/internal/store"
)

const (
	authorizeEndpoint = "https://connect.linux.do/oauth2/authorize"
	tokenEndpoint     = "https://connect.linux.do/oauth2/token"
	userEndpoint      = "https://connect.linux.do/api/user"
)

type Config struct{ ClientID, ClientSecret, CallbackURL string }
type Service struct {
	cfg      Config
	db       *store.Store
	client   *http.Client
	mu       sync.Mutex
	states   map[string]time.Time
	sessions map[string]session
}
type session struct {
	UserID    int64
	ExpiresAt time.Time
}

func New(cfg Config, db *store.Store) *Service {
	return &Service{cfg: cfg, db: db, client: &http.Client{Timeout: 10 * time.Second}, states: map[string]time.Time{}, sessions: map[string]session{}}
}
func (s *Service) Enabled() bool {
	return strings.TrimSpace(s.cfg.ClientID) != "" && strings.TrimSpace(s.cfg.ClientSecret) != "" && strings.TrimSpace(s.cfg.CallbackURL) != ""
}
func randomValue() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func (s *Service) Begin(w http.ResponseWriter) error {
	if !s.Enabled() {
		return errors.New("linuxdo oauth is not configured")
	}
	state, err := randomValue()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cleanupLocked(time.Now())
	s.states[state] = time.Now().Add(10 * time.Minute)
	s.mu.Unlock()
	q := url.Values{"client_id": {s.cfg.ClientID}, "response_type": {"code"}, "redirect_uri": {s.cfg.CallbackURL}, "scope": {"openid profile"}, "state": {state}}
	w.Header().Set("Location", "https://connect.linux.do/oauth2/authorize?"+q.Encode())
	w.WriteHeader(http.StatusFound)
	return nil
}
func (s *Service) Callback(ctx context.Context, code, state string) (store.User, error) {
	if !s.Enabled() {
		return store.User{}, errors.New("linuxdo oauth is not configured")
	}
	s.mu.Lock()
	s.cleanupLocked(time.Now())
	expires, ok := s.states[state]
	delete(s.states, state)
	s.mu.Unlock()
	if !ok || time.Now().After(expires) {
		return store.User{}, errors.New("invalid oauth state")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {s.cfg.CallbackURL}, "client_id": {s.cfg.ClientID}, "client_secret": {s.cfg.ClientSecret}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return store.User{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return store.User{}, fmt.Errorf("oauth token status %d", resp.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil || token.AccessToken == "" {
		return store.User{}, errors.New("invalid oauth token")
	}
	profileReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, userEndpoint, nil)
	profileReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
	profileResp, err := s.client.Do(profileReq)
	if err != nil {
		return store.User{}, err
	}
	defer profileResp.Body.Close()
	if profileResp.StatusCode/100 != 2 {
		return store.User{}, fmt.Errorf("oauth user status %d", profileResp.StatusCode)
	}
	var profile struct {
		ID             any    `json:"id"`
		Username       string `json:"username"`
		Name           string `json:"name"`
		AvatarURL      string `json:"avatar_url"`
		ProfilePicture string `json:"profile_picture"`
	}
	if err := json.NewDecoder(profileResp.Body).Decode(&profile); err != nil {
		return store.User{}, err
	}
	id := fmt.Sprint(profile.ID)
	avatar := profile.AvatarURL
	if avatar == "" {
		avatar = profile.ProfilePicture
	}
	return s.db.UpsertUser(ctx, "linuxdo", id, profile.Username, profile.Name, avatar)
}
func (s *Service) StartSession(user store.User) (string, time.Time, error) {
	token, err := randomValue()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(30 * 24 * time.Hour)
	s.mu.Lock()
	s.cleanupLocked(time.Now())
	s.sessions[token] = session{UserID: user.ID, ExpiresAt: expires}
	s.mu.Unlock()
	return token, expires, nil
}
func (s *Service) UserBySession(token string) (store.User, bool) {
	s.mu.Lock()
	value, ok := s.sessions[token]
	if ok && time.Now().After(value.ExpiresAt) {
		delete(s.sessions, token)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return store.User{}, false
	}
	user, err := s.db.GetUser(context.Background(), value.UserID)
	return user, err == nil
}
func (s *Service) Logout(token string) { s.mu.Lock(); delete(s.sessions, token); s.mu.Unlock() }

func (s *Service) cleanupLocked(now time.Time) {
	for state, expiresAt := range s.states {
		if !expiresAt.After(now) {
			delete(s.states, state)
		}
	}
	for token, value := range s.sessions {
		if !value.ExpiresAt.After(now) {
			delete(s.sessions, token)
		}
	}
}
