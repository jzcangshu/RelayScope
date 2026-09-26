package session

import (
	"encoding/json"
	"errors"
	"strings"
)

// AllAPIHubAccount mirrors one account entry of an All API Hub export file
// (version 4 format). Token fields stay strings; the file is user-supplied.
type AllAPIHubAccount struct {
	SiteName    string `json:"site_name"`
	SiteURL     string `json:"site_url"`
	AuthType    string `json:"authType"`
	AccountInfo struct {
		ID          string `json:"id"`
		AccessToken string `json:"access_token"`
	} `json:"account_info"`
	CookieAuth struct {
		SessionCookie string `json:"sessionCookie"`
	} `json:"cookieAuth"`
}

// ParseAllAPIHubAccounts extracts account entries from an All API Hub
// export. It accepts the whole export file, a bare accounts array, or a
// single account object; entries that do not decode as accounts are skipped.
func ParseAllAPIHubAccounts(body []byte) ([]AllAPIHubAccount, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, errors.New("not a JSON document")
	}
	switch typed := value.(type) {
	case []any:
		return accountsFromSlice(typed), nil
	case map[string]any:
		if _, ok := typed["site_url"]; ok {
			account, err := decodeAllAPIHubAccount(typed)
			if err != nil {
				return nil, err
			}
			return []AllAPIHubAccount{account}, nil
		}
		switch container := typed["accounts"].(type) {
		case []any:
			return accountsFromSlice(container), nil
		case map[string]any:
			if entries, ok := container["accounts"].([]any); ok {
				return accountsFromSlice(entries), nil
			}
		}
	}
	return nil, errors.New("no All API Hub accounts found")
}

func accountsFromSlice(entries []any) []AllAPIHubAccount {
	accounts := make([]AllAPIHubAccount, 0, len(entries))
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if account, err := decodeAllAPIHubAccount(object); err == nil {
			accounts = append(accounts, account)
		}
	}
	return accounts
}

func decodeAllAPIHubAccount(object map[string]any) (AllAPIHubAccount, error) {
	encoded, err := json.Marshal(object)
	if err != nil {
		return AllAPIHubAccount{}, err
	}
	var account AllAPIHubAccount
	if err := json.Unmarshal(encoded, &account); err != nil {
		return AllAPIHubAccount{}, err
	}
	return account, nil
}

// DataFromAllAPIHub converts an All API Hub account entry into session
// credentials; ok is false when the entry carries no usable credentials.
// Access-token accounts keep All API Hub's own auth style (system token plus
// user ID) so RelayScope authenticates exactly like the extension does.
func DataFromAllAPIHub(account AllAPIHubAccount) (Data, bool) {
	switch account.AuthType {
	case "access_token":
		token := strings.TrimSpace(account.AccountInfo.AccessToken)
		userID := strings.TrimSpace(account.AccountInfo.ID)
		if token == "" || userID == "" {
			return Data{}, false
		}
		return Data{AuthType: legacyAccessToken, AccessToken: token, UserID: userID}, true
	case "cookie":
		cookies := ParseCookieHeader(account.CookieAuth.SessionCookie)
		if len(cookies) == 0 {
			return Data{}, false
		}
		return Data{Cookies: cookies}, true
	default:
		return Data{}, false
	}
}

// ParseCookieHeader splits a Cookie request-header string into cookies.
// Pairs without a value are dropped; session cookies always carry one.
func ParseCookieHeader(raw string) []Cookie {
	cookies := make([]Cookie, 0, 4)
	for _, part := range strings.Split(raw, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !found || name == "" || value == "" {
			continue
		}
		cookies = append(cookies, Cookie{Name: name, Value: value})
	}
	return cookies
}
