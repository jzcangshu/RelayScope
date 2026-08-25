package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relayscope/internal/adapter"
	"relayscope/internal/store"
)

func TestProviderInjectsEncryptedSiteSession(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "test", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := NewVault(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(context.Background(), db, site.ID, Data{UserAgent: "saved-agent", Cookies: []Cookie{{Name: "sid", Value: "saved"}}}, nil); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.UserAgent() != "saved-agent" || request.Header.Get("Cookie") != "sid=saved" {
			http.Error(writer, "missing session", http.StatusUnauthorized)
			return
		}
		writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := fetcher.GetBytes(context.Background(), server.URL)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("site session was not injected: body=%s err=%v", body, err)
	}
}

func TestProviderInjectsEncryptedAccessToken(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "test", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := NewVault(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(context.Background(), db, site.ID, Data{AuthType: AuthTypeNewAPIToken, AccessToken: "saved-token", UserID: "42"}, nil); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer saved-token" || request.Header.Get("New-API-User") != "42" || request.Header.Get("X-Api-User") != "42" {
			http.Error(writer, "missing token auth", http.StatusUnauthorized)
			return
		}
		writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := fetcher.GetBytes(context.Background(), server.URL)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("site token was not injected: body=%s err=%v", body, err)
	}
}

func TestProviderRefreshesExpiringSub2APIToken(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/refresh":
			writer.Header().Set("Content-Type", "application/json")
			writer.Write([]byte(`{"code":0,"data":{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600}}`))
		case "/api/v1/model-market":
			if request.Header.Get("Authorization") != "Bearer rotated-access" {
				http.Error(writer, "stale token", http.StatusUnauthorized)
				return
			}
			writer.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "sub2api", BaseURL: server.URL, SourceURL: server.URL + "/model-market", AdapterKey: "model-market", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	rand.Read(key)
	vault, _ := NewVault(base64.RawURLEncoding.EncodeToString(key))
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	if err := vault.Save(context.Background(), db, site.ID, Data{AuthType: AuthTypeSub2APIToken, AccessToken: "old-access", RefreshToken: "old-refresh", TokenExpiresAt: now.Add(time.Minute).UnixMilli()}, nil); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}, Now: func() time.Time { return now }}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fetcher.GetBytes(context.Background(), server.URL+"/api/v1/model-market"); err != nil {
		t.Fatal(err)
	}
	stored, _, err := vault.Load(context.Background(), db, site.ID)
	if err != nil || stored.AccessToken != "rotated-access" || stored.RefreshToken != "rotated-refresh" || stored.TokenExpiresAt != now.Add(time.Hour).UnixMilli() {
		t.Fatalf("rotated credentials were not persisted: token=%t refresh=%t expiry=%d err=%v", stored.AccessToken == "rotated-access", stored.RefreshToken == "rotated-refresh", stored.TokenExpiresAt, err)
	}
}

func TestProviderRefreshesExpiringNewAPITokenWithRefreshCookie(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, time.August, 22, 13, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/user/auth/refresh":
			if request.Method != http.MethodPost || request.Header.Get("Cookie") != "new_api_refresh=refresh-cookie" {
				http.Error(writer, "missing refresh cookie", http.StatusUnauthorized)
				return
			}
			writer.Header().Set("Set-Cookie", "new_api_refresh=rotated-cookie; Path=/api/user/auth")
			writer.Header().Set("Content-Type", "application/json")
			writer.Write([]byte(`{"success":true,"data":{"access_token":"rotated-access","access_expires_at":1787407200}}`))
		case "/api/pricing":
			if request.Header.Get("Authorization") != "Bearer rotated-access" || request.Header.Get("New-API-User") != "42" {
				http.Error(writer, "stale token", http.StatusUnauthorized)
				return
			}
			writer.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "newapi", BaseURL: server.URL, SourceURL: server.URL + "/pricing", AdapterKey: "newapi-pricing", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := NewVault(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(context.Background(), db, site.ID, Data{AuthType: AuthTypeNewAPIToken, AccessToken: "expired-access", UserID: "42", TokenExpiresAt: now.Add(time.Minute).UnixMilli(), Cookies: []Cookie{{Name: "new_api_refresh", Value: "refresh-cookie"}}}, nil); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}, Now: func() time.Time { return now }}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fetcher.GetBytes(context.Background(), server.URL+"/api/pricing"); err != nil {
		t.Fatal(err)
	}
	stored, _, err := vault.Load(context.Background(), db, site.ID)
	if err != nil || stored.AccessToken != "rotated-access" || stored.TokenExpiresAt != 1787407200 || len(stored.Cookies) != 1 || stored.Cookies[0].Value != "rotated-cookie" {
		t.Fatalf("rotated NewAPI credentials were not persisted: token=%t expiry=%d cookie=%v err=%v", stored.AccessToken == "rotated-access", stored.TokenExpiresAt, stored.Cookies, err)
	}
}

func TestProviderScopesStoredSessionToExactBaseOrigin(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	requests := make(chan *http.Request, 2)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Clone(request.Context())
		writer.Write([]byte(`{"ok":true}`))
	})
	baseServer := httptest.NewServer(handler)
	defer baseServer.Close()
	statusServer := httptest.NewServer(handler)
	defer statusServer.Close()

	site, err := db.CreateSite(context.Background(), store.Site{Name: "test", BaseURL: baseServer.URL, SourceURL: statusServer.URL + "/status/ai", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := NewVault(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(context.Background(), db, site.ID, Data{AuthType: AuthTypeNewAPIToken, AccessToken: "saved-token", UserID: "42", UserAgent: "saved-agent", Cookies: []Cookie{{Name: "sid", Value: "saved"}}}, nil); err != nil {
		t.Fatal(err)
	}

	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: baseServer.Client(), UserAgent: "public-agent"}}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: baseServer.URL, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fetcher.GetBytes(context.Background(), baseServer.URL+"/api/pricing"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fetcher.GetBytes(context.Background(), statusServer.URL+"/api/status-page/ai"); err != nil {
		t.Fatal(err)
	}

	baseRequest := <-requests
	statusRequest := <-requests
	if baseRequest.Host != strings.TrimPrefix(baseServer.URL, "http://") {
		baseRequest, statusRequest = statusRequest, baseRequest
	}
	if baseRequest.Header.Get("Authorization") != "Bearer saved-token" || baseRequest.Header.Get("Cookie") != "sid=saved" || baseRequest.UserAgent() != "saved-agent" {
		t.Fatalf("base-origin credentials missing: headers=%v", baseRequest.Header)
	}
	if statusRequest.Header.Get("Authorization") != "" || statusRequest.Header.Get("Cookie") != "" || statusRequest.UserAgent() != "public-agent" {
		t.Fatalf("credentials leaked cross-origin: headers=%v", statusRequest.Header)
	}
}

func TestProviderIgnoresStoredSessionWhenLoginIsNotRequired(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			http.Error(writer, "unexpected session", http.StatusUnauthorized)
			return
		}
		writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "public", BaseURL: server.URL, SourceURL: server.URL + "/status", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := NewVault(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(context.Background(), db, site.ID, Data{AuthType: AuthTypeNewAPIToken, AccessToken: "stale-token", UserID: "42", Cookies: []Cookie{{Name: "sid", Value: "stale"}}}, nil); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: false})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := fetcher.GetBytes(context.Background(), server.URL)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("public fetch used stored session: body=%s err=%v", body, err)
	}
}

// TestProviderRefreshFailureClassifiesCredentialRejection asserts that when a
// NewAPI refresh endpoint rejects the stored cookie (401/403), the error carries
// FetchError.LoginRequired so the collector marks the site login_expired — while
// a transient failure (503) surfaces as a plain error that does not lock the
// site out.
func TestProviderRefreshFailureClassifiesCredentialRejection(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantLogin  bool
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, wantLogin: true},
		{name: "forbidden", statusCode: http.StatusForbidden, wantLogin: true},
		{name: "server error", statusCode: http.StatusServiceUnavailable, wantLogin: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/api/user/auth/refresh" {
					http.NotFound(writer, request)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.statusCode)
				writer.Write([]byte(`{"success":false,"message":"rejected"}`))
			}))
			defer server.Close()
			site, err := db.CreateSite(context.Background(), store.Site{Name: "newapi", BaseURL: server.URL, SourceURL: server.URL + "/pricing", AdapterKey: "newapi-pricing", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			key := make([]byte, 32)
			rand.Read(key)
			vault, _ := NewVault(base64.RawURLEncoding.EncodeToString(key))
			now := time.Date(2026, time.August, 22, 13, 0, 0, 0, time.UTC)
			// Token expired one minute ago, so FetcherForSite must attempt a refresh.
			if err := vault.Save(context.Background(), db, site.ID, Data{AuthType: AuthTypeNewAPIToken, AccessToken: "expired-access", UserID: "42", TokenExpiresAt: now.Add(-time.Minute).UnixMilli(), Cookies: []Cookie{{Name: "new_api_refresh", Value: "refresh-cookie"}}}, nil); err != nil {
				t.Fatal(err)
			}
			provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}, Now: func() time.Time { return now }}
			_, err = provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: true})
			if err == nil {
				t.Fatalf("expected refresh failure, got nil")
			}
			var fetchErr *adapter.FetchError
			loginRequired := errors.As(err, &fetchErr) && fetchErr.LoginRequired
			if loginRequired != test.wantLogin {
				t.Fatalf("refresh %d: error=%v, LoginRequired=%v, want %v", test.statusCode, err, loginRequired, test.wantLogin)
			}
		})
	}
}
