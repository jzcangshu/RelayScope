package session

import "testing"

func TestParseAllAPIHubAccountsAcceptsWholeExport(t *testing.T) {
	body := []byte(`{"version":"4.0","type":"accounts","accounts":{"accounts":[{"site_name":"蛙蛙公益站","site_url":"https://api.feixingwawa.cn","authType":"access_token","account_info":{"id":"504","access_token":"tok"}},{"site_name":"Any","site_url":"https://anyrouter.top","authType":"cookie","cookieAuth":{"sessionCookie":"session=abc; other=def"}}]}}`)
	accounts, err := ParseAllAPIHubAccounts(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(accounts))
	}
	if accounts[0].SiteName != "蛙蛙公益站" || accounts[0].AccountInfo.ID != "504" {
		t.Fatalf("first account = %+v", accounts[0])
	}
}

func TestParseAllAPIHubAccountsAcceptsBareArrayAndSingleAccount(t *testing.T) {
	array := []byte(`[{"site_url":"https://a.example.com","authType":"access_token","account_info":{"id":"1","access_token":"t"}}]`)
	accounts, err := ParseAllAPIHubAccounts(array)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("array parse = %v, %d accounts", err, len(accounts))
	}
	single := []byte(`{"site_url":"https://a.example.com","authType":"access_token","account_info":{"id":"1","access_token":"t"}}`)
	accounts, err = ParseAllAPIHubAccounts(single)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("single parse = %v, %d accounts", err, len(accounts))
	}
}

func TestParseAllAPIHubAccountsRejectsOtherJSON(t *testing.T) {
	for _, body := range []string{`{"accessToken":"x"}`, `not json`, `{"accounts":{"accounts":[]}}`} {
		if accounts, err := ParseAllAPIHubAccounts([]byte(body)); err == nil && len(accounts) > 0 {
			t.Fatalf("%q parsed as %d accounts, want error or empty", body, len(accounts))
		}
	}
}

func TestDataFromAllAPIHub(t *testing.T) {
	data, ok := DataFromAllAPIHub(AllAPIHubAccount{AuthType: "access_token", AccountInfo: struct {
		ID          string `json:"id"`
		AccessToken string `json:"access_token"`
	}{ID: "504", AccessToken: "tok"}})
	if !ok || data.AuthType != legacyAccessToken || data.AccessToken != "tok" || data.UserID != "504" {
		t.Fatalf("access_token map = %+v ok=%v", data, ok)
	}
	if _, ok := DataFromAllAPIHub(AllAPIHubAccount{AuthType: "access_token"}); ok {
		t.Fatal("empty access_token account must not map")
	}
	data, ok = DataFromAllAPIHub(AllAPIHubAccount{AuthType: "cookie", CookieAuth: struct {
		SessionCookie string `json:"sessionCookie"`
	}{SessionCookie: "session=abc; other=def"}})
	if !ok || data.AuthType != "" || len(data.Cookies) != 2 || data.Cookies[0].Name != "session" || data.Cookies[0].Value != "abc" {
		t.Fatalf("cookie map = %+v ok=%v", data, ok)
	}
	if _, ok := DataFromAllAPIHub(AllAPIHubAccount{AuthType: "oauth"}); ok {
		t.Fatal("unknown authType must not map")
	}
}

func TestParseCookieHeader(t *testing.T) {
	cookies := ParseCookieHeader("a=1; b=2=3; broken; ; c=")
	if len(cookies) != 2 || cookies[0].Name != "a" || cookies[0].Value != "1" || cookies[1].Name != "b" || cookies[1].Value != "2=3" {
		t.Fatalf("cookies = %+v", cookies)
	}
}
