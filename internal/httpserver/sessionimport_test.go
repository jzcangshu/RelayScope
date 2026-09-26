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
	"strings"
	"testing"

	"relayscope/internal/admin"
	"relayscope/internal/session"
	"relayscope/internal/store"
)

func TestImportSessionPayloadAcceptsOwnFormat(t *testing.T) {
	payload, err := importSessionPayload([]byte(`{"authType":"access_token","accessToken":"tok","userId":"504"}`), store.Site{BaseURL: "https://api.example.com"})
	if err != nil || payload.AccessToken != "tok" || payload.UserID != "504" {
		t.Fatalf("payload = %+v, err = %v", payload, err)
	}
	payload, err = importSessionPayload([]byte(`{"cookies":[{"name":"session","value":"abc"}]}`), store.Site{BaseURL: "https://api.example.com"})
	if err != nil || len(payload.Cookies) != 1 {
		t.Fatalf("cookie payload = %+v, err = %v", payload, err)
	}
}

func TestImportSessionPayloadAcceptsAllAPIHubExport(t *testing.T) {
	export := []byte(`{"version":"4.0","type":"accounts","accounts":{"accounts":[
		{"site_name":"Other","site_url":"https://other.example.com","authType":"access_token","account_info":{"id":"7","access_token":"t7"}},
		{"site_name":"Mine","site_url":"https://api.example.com/pricing","authType":"access_token","account_info":{"id":"504","access_token":"tok"}}]}}`)
	payload, err := importSessionPayload(export, store.Site{BaseURL: "https://api.example.com"})
	if err != nil || payload.AccessToken != "tok" || payload.UserID != "504" || payload.AuthType != "access_token" {
		t.Fatalf("payload = %+v, err = %v", payload, err)
	}
}

func TestImportSessionPayloadRejectsForeignAccounts(t *testing.T) {
	export := []byte(`{"accounts":{"accounts":[{"site_name":"Other","site_url":"https://other.example.com","authType":"access_token","account_info":{"id":"7","access_token":"t7"}}]}}`)
	if _, err := importSessionPayload(export, store.Site{BaseURL: "https://api.example.com"}); err == nil {
		t.Fatal("expected error for export without this site")
	}
	if _, err := importSessionPayload([]byte(`{"foo":1}`), store.Site{BaseURL: "https://api.example.com"}); err == nil {
		t.Fatal("expected error for unrecognized payload")
	}
}

func TestAdminSessionBatchImportMatchesByOrigin(t *testing.T) {
	db, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	siteOne, err := db.CreateSite(context.Background(), store.Site{Name: "one", BaseURL: "https://one.example.test", SourceURL: "https://one.example.test/pricing", AdapterKey: "test", Enabled: true, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	siteTwo, err := db.CreateSite(context.Background(), store.Site{Name: "two", BaseURL: "https://two.example.test", SourceURL: "https://two.example.test/pricing", AdapterKey: "test", Enabled: true, SessionRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	siteThree, err := db.CreateSite(context.Background(), store.Site{Name: "three", BaseURL: "https://three.example.test", SourceURL: "https://three.example.test/pricing", AdapterKey: "test", Enabled: true, SessionRequired: false})
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
	login := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"this-is-a-long-test-password"}`))
	login.RemoteAddr = "127.0.0.1:12345"
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	var adminCookie, csrfCookie *http.Cookie
	for _, cookie := range loginResponse.Result().Cookies() {
		switch cookie.Name {
		case "relayscope_admin":
			adminCookie = cookie
		case "relayscope_csrf":
			csrfCookie = cookie
		}
	}
	if adminCookie == nil || csrfCookie == nil {
		t.Fatal("login did not issue both session cookies")
	}

	export := `{"version":"4.0","type":"accounts","accounts":{"accounts":[
		{"site_name":"One","site_url":"https://one.example.test/pricing","authType":"access_token","account_info":{"id":"504","access_token":"token-one"}},
		{"site_name":"One again","site_url":"https://one.example.test","authType":"access_token","account_info":{"id":"504","access_token":"token-one-new"}},
		{"site_name":"Two","site_url":"https://two.example.test","authType":"cookie","cookieAuth":{"sessionCookie":"session=abc; other=def"}},
		{"site_name":"Three","site_url":"https://three.example.test","authType":"access_token","account_info":{"id":"7","access_token":"t3"}},
		{"site_name":"Ghost","site_url":"https://ghost.example.test","authType":"access_token","account_info":{"id":"9","access_token":"t9"}}]}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/session-import", strings.NewReader(export))
	request.AddCookie(adminCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("batch import status = %d, body=%s", response.Code, response.Body.String())
	}
	var report struct {
		Imported int `json:"imported"`
		NoMatch  int `json:"noMatch"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Imported != 3 || report.NoMatch != 1 {
		t.Fatalf("report = %+v, want imported 3 noMatch 1", report)
	}
	loaded, _, err := vault.Load(context.Background(), db, siteOne.ID)
	if err != nil || loaded.AccessToken != "token-one-new" || loaded.UserID != "504" {
		t.Fatalf("site one session = %+v, err = %v (duplicate origin must keep the last account)", loaded, err)
	}
	loadedTwo, _, err := vault.Load(context.Background(), db, siteTwo.ID)
	if err != nil || len(loadedTwo.Cookies) != 2 || loadedTwo.Cookies[0].Value != "abc" {
		t.Fatalf("site two session = %+v, err = %v", loadedTwo, err)
	}
	updated, err := db.GetSite(context.Background(), siteThree.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.SessionRequired {
		t.Fatal("importing credentials must enable the session-required flag")
	}
}
