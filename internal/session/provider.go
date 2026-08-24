package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"relayscope/internal/adapter"
	"relayscope/internal/store"
)

type Provider struct {
	Store *store.Store
	Vault *Vault
	Base  adapter.HTTPFetcher
	Now   func() time.Time
}

var sub2APIRefreshMu sync.Mutex
var newAPIRefreshMu sync.Mutex

func (provider Provider) GetJSON(ctx context.Context, rawURL string, target any) error {
	return provider.Base.GetJSON(ctx, rawURL, target)
}
func (provider Provider) GetBytes(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	return provider.Base.GetBytes(ctx, rawURL)
}

func (provider Provider) FetcherForSite(ctx context.Context, site adapter.Site) (adapter.Fetcher, error) {
	if !site.SessionRequired {
		return provider.Base, nil
	}
	if provider.Store == nil || provider.Vault == nil {
		return provider.Base, nil
	}
	data, expires, err := provider.Vault.Load(ctx, provider.Store, site.ID)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// Missing or expired credentials are reported by the normal HTTP path;
		// the collector can still read public sites without a stored session.
		return provider.Base, nil
	}
	now := time.Now().UTC()
	if provider.Now != nil {
		now = provider.Now().UTC()
	}
	if expires != nil && expires.Before(now) {
		return provider.Base, nil
	}
	if data.AuthType == AuthTypeSub2APIToken && tokenExpiry(data.TokenExpiresAt).Before(now.Add(2*time.Minute)) {
		data, err = provider.refreshSub2API(ctx, site, data, now)
		if err != nil {
			return nil, fmt.Errorf("refresh Sub2API credentials: %w", err)
		}
	}
	if data.AuthType == AuthTypeNewAPIToken && hasCookie(data.Cookies, "new_api_refresh") {
		expiresAt := tokenExpiry(data.TokenExpiresAt)
		if data.TokenExpiresAt <= 0 {
			expiresAt = jwtExpiry(data.AccessToken)
		}
		if !expiresAt.IsZero() && expiresAt.Before(now.Add(2*time.Minute)) {
			data, err = provider.refreshNewAPIToken(ctx, site, data, now)
			if err != nil {
				return nil, fmt.Errorf("refresh NewAPI credentials: %w", err)
			}
		}
	}
	cookies := make([]adapter.ChallengeCookie, 0, len(data.Cookies))
	for _, cookie := range data.Cookies {
		cookies = append(cookies, adapter.ChallengeCookie{Name: cookie.Name, Value: cookie.Value})
	}
	base := provider.Base
	if data.UserAgent != "" {
		base.UserAgent = data.UserAgent
	}
	base.Cookies = cookies
	if data.AuthType == legacyAccessToken || data.AuthType == AuthTypeNewAPIToken {
		base.Headers = accessTokenHeaders(data.AccessToken, data.UserID)
	} else if data.AuthType == AuthTypeSub2APIToken {
		base.Headers = map[string]string{"Authorization": "Bearer " + data.AccessToken}
	}
	origin, err := NormalizeOrigin(site.BaseURL)
	if err != nil {
		return provider.Base, nil
	}
	return originScopedFetcher{origin: origin, authenticated: base, public: provider.Base}, nil
}

type originScopedFetcher struct {
	origin        string
	authenticated adapter.Fetcher
	public        adapter.Fetcher
}

func (fetcher originScopedFetcher) GetJSON(ctx context.Context, rawURL string, target any) error {
	return fetcher.forURL(rawURL).GetJSON(ctx, rawURL, target)
}

func (fetcher originScopedFetcher) GetBytes(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	return fetcher.forURL(rawURL).GetBytes(ctx, rawURL)
}

func (fetcher originScopedFetcher) forURL(rawURL string) adapter.Fetcher {
	if origin, err := NormalizeOrigin(rawURL); err == nil && origin == fetcher.origin {
		return fetcher.authenticated
	}
	return fetcher.public
}

type sub2APIRefreshResponse struct {
	Code int `json:"code"`
	Data *struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	} `json:"data"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (provider Provider) refreshSub2API(ctx context.Context, site adapter.Site, current Data, now time.Time) (Data, error) {
	sub2APIRefreshMu.Lock()
	defer sub2APIRefreshMu.Unlock()
	latest, _, err := provider.Vault.Load(ctx, provider.Store, site.ID)
	if err == nil && latest.AuthType == AuthTypeSub2APIToken && tokenExpiry(latest.TokenExpiresAt).After(now.Add(2*time.Minute)) {
		return latest, nil
	}
	endpoint, err := url.JoinPath(site.BaseURL, "/api/v1/auth/refresh")
	if err != nil {
		return Data{}, errors.New("invalid site refresh endpoint")
	}
	payload, err := json.Marshal(map[string]string{"refresh_token": current.RefreshToken})
	if err != nil {
		return Data{}, errors.New("encode refresh request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Data{}, errors.New("build refresh request")
	}
	request.Header.Set("Content-Type", "application/json")
	if provider.Base.UserAgent != "" {
		request.Header.Set("User-Agent", provider.Base.UserAgent)
	}
	client := provider.Base.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return Data{}, fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Data{}, errors.New("read refresh response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Data{}, fmt.Errorf("refresh returned HTTP %d", response.StatusCode)
	}
	var decoded sub2APIRefreshResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Data{}, errors.New("decode refresh response")
	}
	if decoded.Data != nil {
		decoded.AccessToken = decoded.Data.AccessToken
		decoded.RefreshToken = decoded.Data.RefreshToken
		decoded.ExpiresIn = decoded.Data.ExpiresIn
	}
	if strings.TrimSpace(decoded.AccessToken) == "" || strings.TrimSpace(decoded.RefreshToken) == "" || decoded.ExpiresIn <= 0 || decoded.ExpiresIn > int64((365*24*time.Hour)/time.Second) {
		return Data{}, errors.New("refresh response contained invalid credentials")
	}
	current.AccessToken = decoded.AccessToken
	current.RefreshToken = decoded.RefreshToken
	current.TokenExpiresAt = now.Add(time.Duration(decoded.ExpiresIn) * time.Second).UnixMilli()
	if err := provider.Vault.Save(ctx, provider.Store, site.ID, current, nil); err != nil {
		return Data{}, errors.New("persist refreshed credentials")
	}
	return current, nil
}

type newAPIRefreshResponse struct {
	Success bool `json:"success"`
	Data    struct {
		AccessToken     string `json:"access_token"`
		AccessExpiresAt int64  `json:"access_expires_at"`
	} `json:"data"`
}

func (provider Provider) refreshNewAPIToken(ctx context.Context, site adapter.Site, current Data, now time.Time) (Data, error) {
	newAPIRefreshMu.Lock()
	defer newAPIRefreshMu.Unlock()
	latest, _, err := provider.Vault.Load(ctx, provider.Store, site.ID)
	if err == nil && latest.AuthType == AuthTypeNewAPIToken {
		expiresAt := tokenExpiry(latest.TokenExpiresAt)
		if latest.TokenExpiresAt <= 0 {
			expiresAt = jwtExpiry(latest.AccessToken)
		}
		if expiresAt.After(now.Add(2 * time.Minute)) {
			return latest, nil
		}
		current = latest
	}
	endpoint, err := url.JoinPath(site.BaseURL, "/api/user/auth/refresh")
	if err != nil {
		return Data{}, errors.New("invalid NewAPI refresh endpoint")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return Data{}, errors.New("build NewAPI refresh request")
	}
	request.Header.Set("Content-Type", "application/json")
	if current.UserAgent != "" {
		request.Header.Set("User-Agent", current.UserAgent)
	}
	request.Header.Set("Cookie", cookieHeader(current.Cookies))
	client := provider.Base.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return Data{}, fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Data{}, errors.New("read NewAPI refresh response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Data{}, fmt.Errorf("NewAPI refresh returned HTTP %d", response.StatusCode)
	}
	var decoded newAPIRefreshResponse
	if err := json.Unmarshal(body, &decoded); err != nil || !decoded.Success || strings.TrimSpace(decoded.Data.AccessToken) == "" {
		return Data{}, errors.New("NewAPI refresh response contained no access token")
	}
	current.AccessToken = decoded.Data.AccessToken
	current.TokenExpiresAt = decoded.Data.AccessExpiresAt
	if current.TokenExpiresAt <= 0 {
		current.TokenExpiresAt = jwtExpiry(current.AccessToken).UnixMilli()
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "new_api_refresh" {
			current.Cookies = replaceCookie(current.Cookies, Cookie{Name: cookie.Name, Value: cookie.Value})
		}
	}
	if err := provider.Vault.Save(ctx, provider.Store, site.ID, current, nil); err != nil {
		return Data{}, errors.New("persist refreshed NewAPI credentials")
	}
	return current, nil
}

func hasCookie(cookies []Cookie, name string) bool {
	for _, cookie := range cookies {
		if cookie.Name == name && cookie.Value != "" {
			return true
		}
	}
	return false
}

func cookieHeader(cookies []Cookie) string {
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name != "" && cookie.Value != "" {
			values = append(values, cookie.Name+"="+cookie.Value)
		}
	}
	return strings.Join(values, "; ")
}

func replaceCookie(cookies []Cookie, replacement Cookie) []Cookie {
	for index := range cookies {
		if cookies[index].Name == replacement.Name {
			cookies[index] = replacement
			return cookies
		}
	}
	return append(cookies, replacement)
}

func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var payload struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(decoded, &payload) != nil || payload.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(payload.Exp, 0).UTC()
}

func tokenExpiry(value int64) time.Time {
	if value > 100000000000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}

func accessTokenHeaders(token, userID string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + token,
		"New-API-User":  userID,
		"Veloera-User":  userID,
		"X-Api-User":    userID,
		"voapi-user":    userID,
		"User-id":       userID,
		"Rix-Api-User":  userID,
		"neo-api-user":  userID,
		"All-API-Hub":   "true",
	}
}

var _ adapter.SiteFetcher = Provider{}
