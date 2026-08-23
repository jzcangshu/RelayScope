package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"relaypulse/internal/admin"
	"relaypulse/internal/domain"
	"relaypulse/internal/linuxdo"
	"relaypulse/internal/session"
	"relaypulse/internal/store"
)

func TestFeedbackRequiresLinuxDOLoginAndPersistsSubmission(t *testing.T) {
	t.Parallel()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := linuxdo.New(linuxdo.Config{}, db)
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, LinuxDO: service})
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", strings.NewReader(`{"content":"状态不正确"}`))
	unauthenticated.Header.Set("Content-Type", "application/json")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, unauthenticated)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", denied.Code)
	}
	user, err := db.UpsertUser(context.Background(), "linuxdo", "42", "tester", "Tester", "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := service.StartSession(user)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", strings.NewReader(`{"content":"状态不正确"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "relaypulse_user", Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("submission status = %d, body = %s", response.Code, response.Body.String())
	}
	items, err := db.ListFeedback(context.Background(), 10)
	if err != nil || len(items) != 1 || items[0].Content != "状态不正确" || items[0].User.Username != "tester" {
		t.Fatalf("feedback = %#v, err = %v", items, err)
	}
}

func TestHealthAndMetaEndpoints(t *testing.T) {
	t.Parallel()

	handler, err := NewHandler(Options{
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:   "test-version",
		PublicURL: "https://status.example.com",
		Now: func() time.Time {
			return time.Date(2026, time.August, 13, 1, 2, 3, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	tests := []struct {
		path     string
		contains []string
		absent   []string
	}{
		{path: "/health/live", contains: []string{`"status":"ok"`}},
		{path: "/health/ready", contains: []string{`"status":"ready"`}},
		{path: "/api/v1/meta", contains: []string{`"publicUrl":"https://status.example.com"`}},
		{path: "/", contains: []string{
			"RelayPulse - 中转站健康监测",
			`href="/assets/favicon.svg"`,
		}, absent: []string{"expiry-countdown"}},
		{path: "/admin/", contains: []string{"管理员控制台"}},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, expected := range test.contains {
				if !strings.Contains(body, expected) {
					t.Fatalf("body does not contain %q: %s", expected, body)
				}
			}
			for _, forbidden := range test.absent {
				if strings.Contains(body, forbidden) {
					t.Fatalf("body unexpectedly contains %q: %s", forbidden, body)
				}
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("security headers missing")
			}
		})
	}
}

func TestPublicAnnouncementsEndpointListsOnlyActiveFailures(t *testing.T) {
	t.Parallel()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "失败站点", BaseURL: "https://failure.example", SourceURL: "https://failure.example/status", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	runID, err := db.StartCollectionRun(context.Background(), site.ID, "test", started)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishCollectionRun(context.Background(), runID, "failed", false, 0, 0, "challenge_failed", "挑战页未通过", started.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAcquisitionState(context.Background(), site.ID, domain.AcquisitionChallengeFailed, started.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/announcements", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"failureCode":"challenge_failed"`) || !strings.Contains(response.Body.String(), "挑战页未通过") {
		t.Fatalf("announcement response = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminAssetsDisableCaching(t *testing.T) {
	t.Parallel()
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/", "/admin/admin.js"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q, want no-store", path, got)
		}
	}
}

func TestNewHandlerRequiresLogger(t *testing.T) {
	t.Parallel()
	if _, err := NewHandler(Options{}); err == nil {
		t.Fatal("expected logger validation error")
	}
}

func TestAdminSessionImportRequiresAuthAndCSRF(t *testing.T) {
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "test", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "test", Enabled: true, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := admin.NewAuth("this-is-a-long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := session.NewVault(base64.RawURLEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, Auth: auth, SessionVault: vault})
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/admin/sites", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized admin status = %d", unauthorized.Code)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"this-is-a-long-test-password"}`))
	login.RemoteAddr = "127.0.0.1:12345"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var adminCookie, csrfCookie *http.Cookie
	for _, cookie := range loginResponse.Result().Cookies() {
		switch cookie.Name {
		case "relaypulse_admin":
			adminCookie = cookie
		case "relaypulse_csrf":
			csrfCookie = cookie
		}
	}
	if adminCookie == nil || csrfCookie == nil {
		t.Fatal("login did not issue both session cookies")
	}

	forged := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10), strings.NewReader(`{"name":"forged-site","adapterKey":"test-adapter","adapterConfig":"{}","enabled":true,"intervalSeconds":1200,"jitterSeconds":0}`))
	forged.AddCookie(adminCookie)
	forgedCSRF := &http.Cookie{Name: "relaypulse_csrf", Value: "unissued-csrf-token"}
	forged.AddCookie(forgedCSRF)
	forged.Header.Set("X-CSRF-Token", forgedCSRF.Value)
	forgedResponse := httptest.NewRecorder()
	handler.ServeHTTP(forgedResponse, forged)
	if forgedResponse.Code != http.StatusForbidden {
		t.Fatalf("unissued CSRF token status = %d, body=%s", forgedResponse.Code, forgedResponse.Body.String())
	}

	payload := `{"userAgent":"test-agent","cookies":[{"name":"sid","value":"secret-value"}]}`
	withoutCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10)+"/session", strings.NewReader(payload))
	withoutCSRF.AddCookie(adminCookie)
	withoutCSRFResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFResponse, withoutCSRF)
	if withoutCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", withoutCSRFResponse.Code)
	}

	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10)+"/session", strings.NewReader(payload))
	importRequest.AddCookie(adminCookie)
	importRequest.AddCookie(csrfCookie)
	importRequest.Header.Set("X-CSRF-Token", csrfCookie.Value)
	importResponse := httptest.NewRecorder()
	handler.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusOK {
		t.Fatalf("session import status = %d, body=%s", importResponse.Code, importResponse.Body.String())
	}
	loaded, _, err := vault.Load(context.Background(), db, site.ID)
	if err != nil || loaded.Cookies[0].Value != "secret-value" {
		t.Fatalf("stored session cannot be decrypted: %+v %v", loaded, err)
	}
}

func TestAdminCanRenameAndDisableSite(t *testing.T) {
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(context.Background(), store.Site{
		Name:            "original-site",
		BaseURL:         "https://example.test",
		SourceURL:       "https://example.test/pricing",
		AdapterKey:      "test-adapter",
		AdapterConfig:   `{}`,
		Enabled:         true,
		SessionRequired: true,
		Interval:        15 * time.Minute,
		Jitter:          2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := admin.NewAuth("this-is-a-long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"this-is-a-long-test-password"}`))
	login.RemoteAddr = "127.0.0.1:12345"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var adminCookie, csrfCookie *http.Cookie
	for _, cookie := range loginResponse.Result().Cookies() {
		switch cookie.Name {
		case "relaypulse_admin":
			adminCookie = cookie
		case "relaypulse_csrf":
			csrfCookie = cookie
		}
	}
	if adminCookie == nil || csrfCookie == nil {
		t.Fatal("login did not issue both session cookies")
	}

	payload := `{"name":"renamed-site","adapterKey":"test-adapter","adapterConfig":"{}","enabled":false,"intervalSeconds":1200,"jitterSeconds":90}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10), strings.NewReader(payload))
	request.AddCookie(adminCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", response.Code, response.Body.String())
	}

	sites, err := db.ListAllSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("site count = %d, want 1", len(sites))
	}
	updated := sites[0]
	if updated.Name != "renamed-site" || updated.Enabled || !updated.SessionRequired || updated.AdapterKey != "test-adapter" || updated.AdapterConfig != "{}" {
		t.Fatalf("updated site = %+v", updated)
	}
	if updated.Interval != 20*time.Minute || updated.Jitter != 90*time.Second {
		t.Fatalf("updated schedule = interval %s jitter %s", updated.Interval, updated.Jitter)
	}
}

func TestExtensionSessionSyncPairingPendingAndBatchImport(t *testing.T) {
	now := time.Date(2026, time.August, 13, 8, 0, 0, 0, time.UTC)
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(context.Background(), store.Site{Name: "test", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "test", Enabled: true, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAcquisitionState(context.Background(), site.ID, domain.AcquisitionLoginExpired, now); err != nil {
		t.Fatal(err)
	}
	healthySite, err := db.CreateSite(context.Background(), store.Site{Name: "healthy", BaseURL: "https://healthy.example.test", SourceURL: "https://healthy.example.test/pricing", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := admin.NewAuth("this-is-a-long-test-password")
	key := make([]byte, 32)
	rand.Read(key)
	vault, _ := session.NewVault(base64.RawURLEncoding.EncodeToString(key))
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, Auth: auth, SessionVault: vault, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"this-is-a-long-test-password"}`))
	login.RemoteAddr = "127.0.0.1:12345"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	var adminCookie, csrfCookie *http.Cookie
	for _, cookie := range loginResponse.Result().Cookies() {
		if cookie.Name == "relaypulse_admin" {
			adminCookie = cookie
		}
		if cookie.Name == "relaypulse_csrf" {
			csrfCookie = cookie
		}
	}
	pair := httptest.NewRequest(http.MethodPost, "/api/v1/admin/session-sync/pair", nil)
	pair.AddCookie(adminCookie)
	pair.AddCookie(csrfCookie)
	pair.Header.Set("X-CSRF-Token", csrfCookie.Value)
	pairResponse := httptest.NewRecorder()
	handler.ServeHTTP(pairResponse, pair)
	if pairResponse.Code != http.StatusOK {
		t.Fatalf("pair status = %d %s", pairResponse.Code, pairResponse.Body.String())
	}
	var pairing struct {
		Code string `json:"code"`
	}
	json.Unmarshal(pairResponse.Body.Bytes(), &pairing)

	origin := "chrome-extension://abcdefghijklmnopabcdefghijklmnop"
	exchange := httptest.NewRequest(http.MethodPost, "/api/v1/session-sync/exchange", strings.NewReader(`{"code":"`+pairing.Code+`"}`))
	exchange.Header.Set("Origin", origin)
	exchangeResponse := httptest.NewRecorder()
	handler.ServeHTTP(exchangeResponse, exchange)
	if exchangeResponse.Code != http.StatusOK || exchangeResponse.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("exchange status = %d %s", exchangeResponse.Code, exchangeResponse.Body.String())
	}
	var exchanged struct {
		Token string `json:"token"`
	}
	json.Unmarshal(exchangeResponse.Body.Bytes(), &exchanged)

	pending := httptest.NewRequest(http.MethodGet, "/api/v1/session-sync/pending", nil)
	pending.Header.Set("Origin", origin)
	pending.Header.Set("Authorization", "Bearer "+exchanged.Token)
	pendingResponse := httptest.NewRecorder()
	handler.ServeHTTP(pendingResponse, pending)
	if pendingResponse.Code != http.StatusOK || !strings.Contains(pendingResponse.Body.String(), `"origin":"https://example.test"`) {
		t.Fatalf("pending status = %d %s", pendingResponse.Code, pendingResponse.Body.String())
	}
	directPending := httptest.NewRequest(http.MethodGet, "/api/v1/session-sync/pending", nil)
	directPending.Header.Set("Origin", origin)
	directPending.Header.Set("X-RelayPulse-Sync-Password", "this-is-a-long-test-password")
	directPendingResponse := httptest.NewRecorder()
	handler.ServeHTTP(directPendingResponse, directPending)
	if directPendingResponse.Code != http.StatusOK {
		t.Fatalf("direct pending status = %d %s", directPendingResponse.Code, directPendingResponse.Body.String())
	}
	bodyPending := httptest.NewRequest(http.MethodPost, "/api/v1/session-sync/pending", strings.NewReader(`{"password":"this-is-a-long-test-password"}`))
	bodyPending.Header.Set("Origin", origin)
	bodyPendingResponse := httptest.NewRecorder()
	handler.ServeHTTP(bodyPendingResponse, bodyPending)
	if bodyPendingResponse.Code != http.StatusOK {
		t.Fatalf("body pending status = %d %s", bodyPendingResponse.Code, bodyPendingResponse.Body.String())
	}
	bodyBatch := `{"password":"this-is-a-long-test-password","bundles":[{"siteId":` + strconv.FormatInt(site.ID, 10) + `,"origin":"https://example.test","userAgent":"direct-agent","cookies":[{"name":"sid","value":"direct-secret"}]}]}`
	directBatch := httptest.NewRequest(http.MethodPost, "/api/v1/session-sync/batch", strings.NewReader(bodyBatch))
	directBatch.Header.Set("Origin", origin)
	directBatchResponse := httptest.NewRecorder()
	handler.ServeHTTP(directBatchResponse, directBatch)
	if directBatchResponse.Code != http.StatusOK || !strings.Contains(directBatchResponse.Body.String(), `"imported":1`) {
		t.Fatalf("direct batch status = %d %s", directBatchResponse.Code, directBatchResponse.Body.String())
	}
	if err := db.SetAcquisitionState(context.Background(), site.ID, domain.AcquisitionLoginExpired, now); err != nil {
		t.Fatal(err)
	}

	notPendingBody := `{"bundles":[{"siteId":` + strconv.FormatInt(healthySite.ID, 10) + `,"origin":"https://healthy.example.test","cookies":[{"name":"sid","value":"secret"}]}]}`
	notPending := httptest.NewRequest(http.MethodPost, "/api/v1/session-sync/batch", strings.NewReader(notPendingBody))
	notPending.Header.Set("Origin", origin)
	notPending.Header.Set("Authorization", "Bearer "+exchanged.Token)
	notPendingResponse := httptest.NewRecorder()
	handler.ServeHTTP(notPendingResponse, notPending)
	if notPendingResponse.Code != http.StatusBadRequest {
		t.Fatalf("non-pending site batch status = %d", notPendingResponse.Code)
	}

	body := `{"bundles":[{"siteId":` + strconv.FormatInt(site.ID, 10) + `,"origin":"https://example.test","userAgent":"extension-agent","cookies":[{"name":"sid","value":"secret"}]}]}`
	batch := httptest.NewRequest(http.MethodPost, "/api/v1/session-sync/batch", strings.NewReader(body))
	batch.Header.Set("Origin", origin)
	batch.Header.Set("Authorization", "Bearer "+exchanged.Token)
	batchResponse := httptest.NewRecorder()
	handler.ServeHTTP(batchResponse, batch)
	if batchResponse.Code != http.StatusOK || !strings.Contains(batchResponse.Body.String(), `"imported":1`) {
		t.Fatalf("batch status = %d %s", batchResponse.Code, batchResponse.Body.String())
	}
	loaded, _, err := vault.Load(context.Background(), db, site.ID)
	if err != nil || loaded.UserAgent != "extension-agent" || loaded.Cookies[0].Value != "secret" {
		t.Fatalf("batch session not saved: %+v %v", loaded, err)
	}

	replay := httptest.NewRequest(http.MethodPost, "/api/v1/session-sync/batch", strings.NewReader(body))
	replay.Header.Set("Origin", origin)
	replay.Header.Set("Authorization", "Bearer "+exchanged.Token)
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusUnauthorized {
		t.Fatalf("consumed token replay status = %d", replayResponse.Code)
	}
}

func TestPublicSitesExcludeAdministrativeFields(t *testing.T) {
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.CreateSite(context.Background(), store.Site{Name: "test", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "private-adapter", AdapterConfig: `{"private":"configuration"}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sites", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("public sites status = %d", response.Code)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	text := response.Body.String()
	for _, forbidden := range []string{"adapterKey", "adapterConfig", "sessionConfigured", "private-adapter", "configuration"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public sites leaked %q: %s", forbidden, text)
		}
	}
}

func TestPendingSessionSitesIncludesCredentialVerificationFailureOnly(t *testing.T) {
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	privateSite, err := db.CreateSite(context.Background(), store.Site{Name: "private", BaseURL: "https://private.example.test", SourceURL: "https://private.example.test/model-market", AdapterKey: "model-market", Enabled: true, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	publicSite, err := db.CreateSite(context.Background(), store.Site{Name: "public", BaseURL: "https://public.example.test", SourceURL: "https://public.example.test/pricing", AdapterKey: "newapi-pricing", Enabled: true, SessionRequired: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, siteID := range []int64{privateSite.ID, publicSite.ID} {
		if err := db.SetAcquisitionState(context.Background(), siteID, domain.AcquisitionCollectionFailed, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SaveEncryptedSession(context.Background(), store.EncryptedSession{SiteID: privateSite.ID, Purpose: session.SessionPurpose, KeyVersion: 1, Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext")}); err != nil {
		t.Fatal(err)
	}
	items, err := pendingSessionSites(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != privateSite.ID || items[0].Reason != "verification_failed" {
		t.Fatalf("pending sites = %+v, want only credential verification failure", items)
	}
}

func TestPendingSessionSitesUsesBaseOriginForAuthenticatedPricing(t *testing.T) {
	now := time.Date(2026, time.August, 16, 8, 0, 0, 0, time.UTC)
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(context.Background(), store.Site{
		Name: "cross-origin", BaseURL: "https://pricing.example.test", SourceURL: "https://status.example.test/status/ai",
		AdapterKey: "uptime-kuma", AdapterConfig: `{"statusBaseUrl":"https://status.example.test","pricingRequiresSession":true}`, Enabled: true, SessionRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := pendingSessionSites(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != site.ID || items[0].Origin != "https://pricing.example.test" || items[0].LoginURL != "https://pricing.example.test" || items[0].Reason != "login_required" {
		t.Fatalf("pending pricing site = %+v", items)
	}
}

func TestPublicDetailsRequiresScopedModelAndDefaultsToTwentyFourHours(t *testing.T) {
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, Now: func() time.Time { return time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/details", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unscoped details response = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/details?site=test&raw=model", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"hours":24`) || !strings.Contains(response.Body.String(), `"groups":`) {
		t.Fatalf("scoped details response = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminSiteLifecycleFiltersAndRedactedSessionMetadata(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(ctx, store.Site{Name: "lifecycle", BaseURL: "https://lifecycle.example", SourceURL: "https://lifecycle.example/status", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := db.StartCollectionRun(ctx, site.ID, "test", time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishCollectionRun(ctx, runID, "failed", false, 0, 0, "test_failure", "not exposed", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEncryptedSession(ctx, store.EncryptedSession{SiteID: site.ID, Purpose: session.SessionPurpose, KeyVersion: 1, Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext"), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	auth, err := admin.NewAuth("this-is-a-long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"this-is-a-long-test-password"}`))
	login.RemoteAddr = "127.0.0.1:12345"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	var adminCookie, csrfCookie *http.Cookie
	for _, cookie := range loginResponse.Result().Cookies() {
		if cookie.Name == "relaypulse_admin" {
			adminCookie = cookie
		}
		if cookie.Name == "relaypulse_csrf" {
			csrfCookie = cookie
		}
	}
	if adminCookie == nil || csrfCookie == nil {
		t.Fatal("login did not issue cookies")
	}
	request := func(method, path string, body io.Reader, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		req.AddCookie(adminCookie)
		req.AddCookie(csrfCookie)
		if csrf {
			req.Header.Set("X-CSRF-Token", csrfCookie.Value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	metadata := request(http.MethodGet, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10)+"/session", nil, false)
	if metadata.Code != http.StatusOK || strings.Contains(metadata.Body.String(), `"ciphertext":"`) || strings.Contains(metadata.Body.String(), `"nonce":"`) {
		t.Fatalf("session metadata leaked or failed: %d %s", metadata.Code, metadata.Body.String())
	}
	runs := request(http.MethodGet, "/api/v1/admin/runs?status=failed&limit=1", nil, false)
	if runs.Code != http.StatusOK || !strings.Contains(runs.Body.String(), `"status":"failed"`) {
		t.Fatalf("filtered runs = %d %s", runs.Code, runs.Body.String())
	}
	deleted := request(http.MethodDelete, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10), nil, true)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d %s", deleted.Code, deleted.Body.String())
	}
	deletedMetadata := request(http.MethodGet, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10)+"/session", nil, false)
	if deletedMetadata.Code != http.StatusNotFound {
		t.Fatalf("deleted session metadata status = %d %s", deletedMetadata.Code, deletedMetadata.Body.String())
	}
	deletedUpdate := request(http.MethodPatch, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10), strings.NewReader(`{"name":"should-not-update","adapterKey":"test","adapterConfig":"{}","enabled":true,"intervalSeconds":900,"jitterSeconds":0}`), true)
	if deletedUpdate.Code != http.StatusBadRequest {
		t.Fatalf("deleted site update status = %d %s", deletedUpdate.Code, deletedUpdate.Body.String())
	}
	visible := request(http.MethodGet, "/api/v1/admin/sites", nil, false)
	if visible.Code != http.StatusOK || strings.Contains(visible.Body.String(), "lifecycle") {
		t.Fatalf("deleted site remains visible: %d %s", visible.Code, visible.Body.String())
	}
	restored := request(http.MethodPost, "/api/v1/admin/sites/"+strconv.FormatInt(site.ID, 10)+"/restore", nil, true)
	if restored.Code != http.StatusOK {
		t.Fatalf("restore status = %d %s", restored.Code, restored.Body.String())
	}
	logout := request(http.MethodPost, "/api/v1/admin/logout", nil, true)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status = %d %s", logout.Code, logout.Body.String())
	}
	unauthorized := request(http.MethodGet, "/api/v1/admin/sites", nil, false)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status = %d", unauthorized.Code)
	}
}

func TestPublicDashboardReturnsCachedSnapshotShape(t *testing.T) {
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, Now: func() time.Time { return time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	for requestNumber := 0; requestNumber < 2; requestNumber++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/dashboard", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("dashboard response %d = %d %s", requestNumber, response.Code, response.Body.String())
		}
		text := response.Body.String()
		for _, field := range []string{`"revision":0`, `"hours":24`, `"rows":[]`, `"buckets":[]`} {
			if !strings.Contains(text, field) {
				t.Fatalf("dashboard response missing %s: %s", field, text)
			}
		}
	}
	if err := db.BumpRevision(context.Background()); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/dashboard", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"revision":1`) {
		t.Fatalf("dashboard cache did not invalidate: %d %s", response.Code, response.Body.String())
	}
}
