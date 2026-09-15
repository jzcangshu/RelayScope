package linuxdo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relayscope/internal/store"
)

func newFakeConnect(t *testing.T, profile map[string]any) (*Service, *store.Store) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "the-code" || r.Form.Get("client_secret") != "secret" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at-1"})
	})
	mux.HandleFunc("GET /api/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(profile)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	previousToken, previousUser := tokenEndpoint, userEndpoint
	tokenEndpoint = server.URL + "/oauth2/token"
	userEndpoint = server.URL + "/api/user"
	t.Cleanup(func() { tokenEndpoint, userEndpoint = previousToken, previousUser })

	db, err := store.Open(context.Background(), t.TempDir()+"/oauth.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := New(Config{ClientID: "id", ClientSecret: "secret", CallbackURL: "https://watchbot.cfd/api/v1/auth/linuxdo/callback"}, db)
	return service, db
}

func TestBeginRedirectsWithState(t *testing.T) {
	service, _ := newFakeConnect(t, map[string]any{})
	recorder := httptest.NewRecorder()
	if err := service.Begin(recorder); err != nil {
		t.Fatal(err)
	}
	location := recorder.Header().Get("Location")
	if !strings.HasPrefix(location, authorizeEndpoint) || !strings.Contains(location, "client_id=id") || !strings.Contains(location, "state=") {
		t.Fatalf("unexpected authorize redirect: %s", location)
	}
}

func TestCallbackUpsertsProfileWithTrustLevelAndStartsSession(t *testing.T) {
	service, db := newFakeConnect(t, map[string]any{"id": 42, "username": "tester", "name": "Tester", "avatar_url": "https://a/x.png", "trust_level": 2})
	service.mu.Lock()
	service.states["state-1"] = time.Now().Add(time.Minute)
	service.mu.Unlock()
	user, err := service.Callback(context.Background(), "the-code", "state-1")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "tester" || user.TrustLevel != 2 || user.AvatarURL != "https://a/x.png" {
		t.Fatalf("profile mismatch: %+v", user)
	}
	token, expires, err := service.StartSession(user)
	if err != nil || !expires.After(time.Now()) {
		t.Fatalf("start session: %v %v", token, err)
	}
	if got, ok := service.UserBySession(token); !ok || got.ID != user.ID {
		t.Fatal("session should resolve user")
	}
	// 会话落库：新建 Service（模拟进程重启）后依然有效
	restarted := New(Config{ClientID: "id", ClientSecret: "secret", CallbackURL: "cb"}, db)
	if got, ok := restarted.UserBySession(token); !ok || got.Username != "tester" {
		t.Fatal("session must survive restart")
	}
	restarted.Logout(token)
	if _, ok := restarted.UserBySession(token); ok {
		t.Fatal("logout must invalidate session")
	}
}

func TestCallbackRejectsUnknownState(t *testing.T) {
	service, _ := newFakeConnect(t, map[string]any{})
	if _, err := service.Callback(context.Background(), "the-code", "bogus"); err == nil {
		t.Fatal("unknown state must be rejected")
	}
}
