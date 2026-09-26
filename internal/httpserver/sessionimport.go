package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"relayscope/internal/session"
	"relayscope/internal/store"
)

// sessionImportResult reports the outcome for one All API Hub account entry.
type sessionImportResult struct {
	SiteName string `json:"siteName"`
	SiteURL  string `json:"siteUrl"`
	Status   string `json:"status"` // imported | no_match | skipped
	Detail   string `json:"detail,omitempty"`
}

// ensureSessionRequired flips the site's session flag when credentials are
// imported for a site not marked as requiring login. Importing credentials is
// explicit evidence that the collector should use them, and the extension's
// pending list only offers session-required sites, so leaving the flag off
// would silently ignore everything that was just imported.
func ensureSessionRequired(ctx context.Context, db *store.Store, site store.Site) error {
	if site.SessionRequired {
		return nil
	}
	required := true
	return db.UpdateSite(ctx, site.ID, site.Name, site.AdapterKey, site.AdapterConfig, site.Enabled, &required, site.Interval, site.Jitter)
}

// importSessionPayload accepts both RelayScope's own session JSON and All
// API Hub export data (whole file or a single account entry); for All API
// Hub data the account entry matching the site's origin is used.
func importSessionPayload(body []byte, site store.Site) (session.Data, error) {
	var payload session.Data
	if err := json.Unmarshal(body, &payload); err == nil && (payload.AccessToken != "" || len(payload.Cookies) > 0) {
		return payload, nil
	}
	accounts, err := session.ParseAllAPIHubAccounts(body)
	if err != nil {
		return session.Data{}, errors.New("登录态内容无法识别：需要 accessToken/userId 或 All API Hub 账号数据")
	}
	siteOrigin, err := session.NormalizeOrigin(site.BaseURL)
	if err != nil {
		return session.Data{}, errors.New("站点地址无法解析")
	}
	for _, account := range accounts {
		accountOrigin, err := session.NormalizeOrigin(account.SiteURL)
		if err == nil && strings.EqualFold(accountOrigin, siteOrigin) {
			if data, ok := session.DataFromAllAPIHub(account); ok {
				return data, nil
			}
			return session.Data{}, errors.New("该站点的 All API Hub 账号缺少可用凭据")
		}
	}
	return session.Data{}, errors.New("导出文件中没有此站点的账号")
}
