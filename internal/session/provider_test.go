package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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

func TestProviderRecoversSub2API401WithRotatedCredentials(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	refreshes := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/refresh":
			refreshes++
			if request.Header.Get("Authorization") != "" {
				t.Errorf("refresh request must not carry the rejected access token")
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.Write([]byte(`{"code":0,"data":{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":7200}}`))
		case "/api/v1/channel-monitors":
			if request.Header.Get("Authorization") != "Bearer fresh-access" {
				http.Error(writer, "stale token", http.StatusUnauthorized)
				return
			}
			writer.Write([]byte(`{"code":0,"data":{"items":[]}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "sub2api", BaseURL: server.URL, SourceURL: server.URL + "/monitor", AdapterKey: "sub2api-monitor", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	rand.Read(key)
	vault, _ := NewVault(base64.RawURLEncoding.EncodeToString(key))
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	// Token far from its recorded expiry: only the passive 401 path recovers.
	if err := vault.Save(context.Background(), db, site.ID, Data{AuthType: AuthTypeSub2APIToken, AccessToken: "stale-access", RefreshToken: "stale-refresh", TokenExpiresAt: now.Add(time.Hour).UnixMilli()}, nil); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}, Now: func() time.Time { return now }}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := fetcher.GetBytes(context.Background(), server.URL+"/api/v1/channel-monitors")
	if err != nil || string(body) != `{"code":0,"data":{"items":[]}}` {
		t.Fatalf("request was not recovered after 401: body=%s err=%v", body, err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshes)
	}
	stored, _, err := vault.Load(context.Background(), db, site.ID)
	if err != nil || stored.AccessToken != "fresh-access" || stored.RefreshToken != "fresh-refresh" {
		t.Fatalf("rotated credentials were not persisted: %+v err=%v", stored, err)
	}
	// A follow-up request uses the rotated token without another refresh.
	if _, _, err := fetcher.GetBytes(context.Background(), server.URL+"/api/v1/channel-monitors"); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh calls after retry = %d, want 1", refreshes)
	}
}

func TestProviderPassiveRefreshRejectionKeepsLoginRequired(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	refreshes := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/refresh":
			refreshes++
			http.Error(writer, `{"code":401,"message":"invalid refresh token"}`, http.StatusUnauthorized)
		case "/api/v1/channel-monitors":
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "sub2api", BaseURL: server.URL, SourceURL: server.URL + "/monitor", AdapterKey: "sub2api-monitor", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	rand.Read(key)
	vault, _ := NewVault(base64.RawURLEncoding.EncodeToString(key))
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	if err := vault.Save(context.Background(), db, site.ID, Data{AuthType: AuthTypeSub2APIToken, AccessToken: "stale-access", RefreshToken: "dead-refresh", TokenExpiresAt: now.Add(time.Hour).UnixMilli()}, nil); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}, Now: func() time.Time { return now }}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = fetcher.GetBytes(context.Background(), server.URL+"/api/v1/channel-monitors")
	var fetchErr *adapter.FetchError
	if !errors.As(err, &fetchErr) || fetchErr.StatusCode != http.StatusUnauthorized || !fetchErr.LoginRequired {
		t.Fatalf("expected login-required fetch error, got %v", err)
	}
	if refreshes != 1 {
		t.Fatalf("refresh calls = %d, want exactly 1 (no retry loop)", refreshes)
	}
	stored, _, err := vault.Load(context.Background(), db, site.ID)
	if err != nil || stored.RefreshToken != "dead-refresh" {
		t.Fatalf("rejected refresh token must not be replaced: %+v err=%v", stored, err)
	}
}

func TestProviderPassiveRefreshReusesConcurrentRotation(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	refreshes := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/refresh":
			refreshes++
			writer.Header().Set("Content-Type", "application/json")
			writer.Write([]byte(`{"code":0,"data":{"access_token":"other-access","refresh_token":"other-refresh","expires_in":3600}}`))
		case "/api/v1/channel-monitors":
			if request.Header.Get("Authorization") != "Bearer concurrent-access" {
				http.Error(writer, "stale token", http.StatusUnauthorized)
				return
			}
			writer.Write([]byte(`{"code":0,"data":{"items":[]}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "sub2api", BaseURL: server.URL, SourceURL: server.URL + "/monitor", AdapterKey: "sub2api-monitor", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	rand.Read(key)
	vault, _ := NewVault(base64.RawURLEncoding.EncodeToString(key))
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	seeded := Data{AuthType: AuthTypeSub2APIToken, AccessToken: "seed-access", RefreshToken: "seed-refresh", TokenExpiresAt: now.Add(time.Hour).UnixMilli()}
	if err := vault.Save(context.Background(), db, site.ID, seeded, nil); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Store: db, Vault: vault, Base: adapter.HTTPFetcher{Client: server.Client()}, Now: func() time.Time { return now }}
	fetcher, err := provider.FetcherForSite(context.Background(), adapter.Site{ID: site.ID, BaseURL: server.URL, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	// Another collector rotates the credentials before the request fires.
	concurrent := Data{AuthType: AuthTypeSub2APIToken, AccessToken: "concurrent-access", RefreshToken: "concurrent-refresh", TokenExpiresAt: now.Add(2 * time.Hour).UnixMilli()}
	if err := vault.Save(context.Background(), db, site.ID, concurrent, nil); err != nil {
		t.Fatal(err)
	}
	body, _, err := fetcher.GetBytes(context.Background(), server.URL+"/api/v1/channel-monitors")
	if err != nil || string(body) != `{"code":0,"data":{"items":[]}}` {
		t.Fatalf("concurrent rotation was not reused: body=%s err=%v", body, err)
	}
	if refreshes != 0 {
		t.Fatalf("refresh calls = %d, want 0 (stored rotation already newer)", refreshes)
	}
}

func TestExchangeSub2APITokenBuildsStorableCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/auth/refresh" {
			http.NotFound(writer, request)
			return
		}
		var payload struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.RefreshToken != "rt_input" {
			http.Error(writer, "bad payload", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Write([]byte(`{"code":0,"data":{"access_token":"exchanged-access","refresh_token":"rt_next","expires_in":86400}}`))
	}))
	defer server.Close()

	vault, err := NewVault(base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	data, err := ExchangeSub2APIToken(context.Background(), server.Client(), server.URL, Data{RefreshToken: "rt_input"})
	if err != nil {
		t.Fatal(err)
	}
	if data.AuthType != AuthTypeSub2APIToken || data.AccessToken != "exchanged-access" || data.RefreshToken != "rt_next" || data.TokenExpiresAt <= 0 {
		t.Fatalf("exchanged data = %+v", data)
	}
	if _, _, err := vault.Encrypt(data); err != nil {
		t.Fatalf("exchanged credentials must pass validation: %v", err)
	}

	// Rejection envelopes surface as login-required failures.
	rejecting := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Write([]byte(`{"code":"SESSION_BINDING_MISMATCH","message":"session network fingerprint changed"}`))
	}))
	defer rejecting.Close()
	if _, err := ExchangeSub2APIToken(context.Background(), rejecting.Client(), rejecting.URL, Data{RefreshToken: "rt_input"}); err == nil {
		t.Fatal("expected binding mismatch rejection")
	} else {
		var fetchErr *adapter.FetchError
		if !errors.As(err, &fetchErr) || !fetchErr.LoginRequired {
			t.Fatalf("binding mismatch must be login-required: %v", err)
		}
	}
}

