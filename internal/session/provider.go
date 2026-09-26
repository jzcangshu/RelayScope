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
func (provider Provider) PostJSON(ctx context.Context, rawURL string, body any) ([]byte, http.Header, error) {
	return provider.Base.PostJSON(ctx, rawURL, body)
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
	origin, err := NormalizeOrigin(site.BaseURL)
	if err != nil {
		return provider.Base, nil
	}
	base := provider.authenticatedFetcher(data)
	if data.AuthType == AuthTypeSub2APIToken {
		// Sub2API access tokens can be rejected before our recorded expiry
		// (upstream rotation, family revocation, deployments that invalidate
		// sessions on restart). Recover once per request by force-refreshing
		// the stored refresh token — All API Hub recovers the same way — and
		// replaying the failed request with the rotated credentials.
		recovery := func(ctx context.Context) (adapter.HTTPFetcher, bool) {
			now := time.Now().UTC()
			if provider.Now != nil {
				now = provider.Now().UTC()
			}
			refreshed, err := provider.forceRefreshSub2API(ctx, site, data, now)
			if err != nil {
				return adapter.HTTPFetcher{}, false
			}
			return provider.authenticatedFetcher(refreshed), true
		}
		return originScopedFetcher{origin: origin, authenticated: recoveringFetcher{HTTPFetcher: base, recoverUnauthorized: recovery}, public: provider.Base}, nil
	}
	return originScopedFetcher{origin: origin, authenticated: base, public: provider.Base}, nil
}

// authenticatedFetcher builds the origin fetcher that presents the stored
// credentials (cookies plus token headers) for the given session data.
func (provider Provider) authenticatedFetcher(data Data) adapter.HTTPFetcher {
	base := provider.Base
	if data.UserAgent != "" {
		base.UserAgent = data.UserAgent
	}
	base.Cookies = make([]adapter.ChallengeCookie, 0, len(data.Cookies))
	for _, cookie := range data.Cookies {
		base.Cookies = append(base.Cookies, adapter.ChallengeCookie{Name: cookie.Name, Value: cookie.Value})
	}
	switch data.AuthType {
	case legacyAccessToken, AuthTypeNewAPIToken:
		base.Headers = accessTokenHeaders(data.AccessToken, data.UserID)
	case AuthTypeSub2APIToken:
		base.Headers = map[string]string{"Authorization": "Bearer " + data.AccessToken}
	}
	return base
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

func (fetcher originScopedFetcher) PostJSON(ctx context.Context, rawURL string, body any) ([]byte, http.Header, error) {
	delegate, ok := fetcher.forURL(rawURL).(adapter.JSONPoster)
	if !ok {
		return nil, nil, &adapter.FetchError{URL: rawURL, Err: errors.New("fetcher does not support POST requests")}
	}
	return delegate.PostJSON(ctx, rawURL, body)
}

func (fetcher originScopedFetcher) forURL(rawURL string) adapter.Fetcher {
	if origin, err := NormalizeOrigin(rawURL); err == nil && origin == fetcher.origin {
		return fetcher.authenticated
	}
	return fetcher.public
}

// recoveringFetcher presents credentials and, when the site answers 401,
// rotates them once through recoverUnauthorized and replays the request. The
// wrapped HTTPFetcher methods are re-declared because Go method values bind
// to the embedded receiver: the embedded GetJSON would otherwise bypass the
// overridden GetBytes.
type recoveringFetcher struct {
	adapter.HTTPFetcher
	recoverUnauthorized func(ctx context.Context) (adapter.HTTPFetcher, bool)
}

func recoveringError(err error) bool {
	var fetchErr *adapter.FetchError
	return errors.As(err, &fetchErr) && fetchErr.StatusCode == http.StatusUnauthorized
}

func (fetcher recoveringFetcher) GetBytes(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	body, header, err := fetcher.HTTPFetcher.GetBytes(ctx, rawURL)
	if !recoveringError(err) {
		return body, header, err
	}
	if retry, ok := fetcher.recoverUnauthorized(ctx); ok {
		return retry.GetBytes(ctx, rawURL)
	}
	return body, header, err
}

func (fetcher recoveringFetcher) GetJSON(ctx context.Context, rawURL string, target any) error {
	err := fetcher.HTTPFetcher.GetJSON(ctx, rawURL, target)
	if !recoveringError(err) {
		return err
	}
	if retry, ok := fetcher.recoverUnauthorized(ctx); ok {
		return retry.GetJSON(ctx, rawURL, target)
	}
	return err
}

type sub2APIRefreshResponse struct {
	Code    any `json:"code"`
	Message string `json:"message"`
	Data *struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	} `json:"data"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// rejection reports an in-envelope credential rejection (code != 0), whose
// code may be a numeric or string constant. Sub2API deployments reply with
// HTTP 200 and SESSION_BINDING_MISMATCH when the token family is bound to a
// different client fingerprint, and with HTTP 401 for rejected tokens.
func (response sub2APIRefreshResponse) rejection() (string, string) {
	switch code := response.Code.(type) {
	case string:
		if code != "" && code != "0" {
			return code, response.Message
		}
	case float64:
		if code != 0 {
			return fmt.Sprintf("%d", int64(code)), response.Message
		}
	}
	return "", ""
}

func (provider Provider) refreshSub2API(ctx context.Context, site adapter.Site, current Data, now time.Time) (Data, error) {
	return provider.rotateSub2API(ctx, site, current, now, false)
}

// forceRefreshSub2API rotates credentials after an authentication failure. The
// stored refresh token may already have been rotated by a concurrent
// collector or a proactive refresh; in that case the freshly stored
// credentials are returned without another network round-trip, since the
// failed request simply raced that rotation and upstream rejects replayed
// refresh tokens.
func (provider Provider) forceRefreshSub2API(ctx context.Context, site adapter.Site, current Data, now time.Time) (Data, error) {
	return provider.rotateSub2API(ctx, site, current, now, true)
}

func (provider Provider) rotateSub2API(ctx context.Context, site adapter.Site, current Data, now time.Time, force bool) (Data, error) {
	sub2APIRefreshMu.Lock()
	defer sub2APIRefreshMu.Unlock()
	latest, _, err := provider.Vault.Load(ctx, provider.Store, site.ID)
	if err == nil && latest.AuthType == AuthTypeSub2APIToken && strings.TrimSpace(latest.RefreshToken) != "" {
		if latest.RefreshToken != current.RefreshToken && tokenExpiry(latest.TokenExpiresAt).After(now) {
			return latest, nil
		}
		current = latest
		if !force && tokenExpiry(current.TokenExpiresAt).After(now.Add(2*time.Minute)) {
			return current, nil
		}
	}
	endpoint, err := url.JoinPath(site.BaseURL, "/api/v1/auth/refresh")
	if err != nil {
		return Data{}, errors.New("invalid site refresh endpoint")
	}
	client := provider.Base.Client
	if client == nil {
		client = http.DefaultClient
	}
	accessToken, refreshToken, expiresIn, err := exchangeSub2APIToken(ctx, client, endpoint, current.RefreshToken, provider.Base.UserAgent)
	if err != nil {
		return Data{}, err
	}
	if expiresIn <= 0 || expiresIn > int64((365*24*time.Hour)/time.Second) {
		return Data{}, errors.New("refresh response contained invalid credentials")
	}
	current.AccessToken = accessToken
	current.RefreshToken = refreshToken
	current.TokenExpiresAt = now.Add(time.Duration(expiresIn) * time.Second).UnixMilli()
	if err := provider.Vault.Save(ctx, provider.Store, site.ID, current, nil); err != nil {
		return Data{}, errors.New("persist refreshed credentials")
	}
	return current, nil
}

// ExchangeSub2APIToken rotates a bare Sub2API refresh token into a complete
// credential pair for the site, ready to store. Imports use it because the
// upstream invalidates the submitted refresh token on every exchange, so the
// rotated pair must be persisted immediately.
func ExchangeSub2APIToken(ctx context.Context, client *http.Client, baseURL string, base Data) (Data, error) {
	refreshToken := strings.TrimSpace(base.RefreshToken)
	if refreshToken == "" {
		return Data{}, errors.New("refresh token is required")
	}
	endpoint, err := url.JoinPath(baseURL, "/api/v1/auth/refresh")
	if err != nil {
		return Data{}, errors.New("invalid site refresh endpoint")
	}
	accessToken, refreshToken, expiresIn, err := exchangeSub2APIToken(ctx, client, endpoint, refreshToken, base.UserAgent)
	if err != nil {
		return Data{}, err
	}
	now := time.Now().UTC()
	return Data{
		AuthType:       AuthTypeSub2APIToken,
		AccessToken:    accessToken,
		RefreshToken:   refreshToken,
		TokenExpiresAt: now.Add(time.Duration(expiresIn) * time.Second).UnixMilli(),
		UserAgent:      base.UserAgent,
	}, nil
}

// exchangeSub2APIToken posts the refresh token to the site's refresh endpoint
// and returns the rotated credential pair (access token, refresh token,
// expires-in seconds). Upstream rotates on every use: the submitted refresh
// token is invalidated immediately and a complete replacement pair is
// returned, so an incomplete success response is an uncertain credential
// mutation and callers must not replay the submitted token.
func exchangeSub2APIToken(ctx context.Context, client *http.Client, endpoint, refreshToken, userAgent string) (string, string, int64, error) {
	payload, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return "", "", 0, errors.New("encode refresh request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", "", 0, errors.New("build refresh request")
	}
	request.Header.Set("Content-Type", "application/json")
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", "", 0, fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return "", "", 0, errors.New("read refresh response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", 0, refreshFailure(response.StatusCode, "Sub2API refresh returned HTTP %d")
	}
	var decoded sub2APIRefreshResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", "", 0, errors.New("decode refresh response")
	}
	nextAccessToken := decoded.AccessToken
	nextRefreshToken := decoded.RefreshToken
	nextExpiresIn := decoded.ExpiresIn
	if decoded.Data != nil {
		nextAccessToken = decoded.Data.AccessToken
		nextRefreshToken = decoded.Data.RefreshToken
		nextExpiresIn = decoded.Data.ExpiresIn
	}
	if strings.TrimSpace(nextAccessToken) == "" || strings.TrimSpace(nextRefreshToken) == "" || nextExpiresIn <= 0 {
		if code, message := decoded.rejection(); code != "" {
			return "", "", 0, &adapter.FetchError{StatusCode: http.StatusUnauthorized, LoginRequired: true, Err: errors.New("Sub2API refresh rejected (" + code + "): " + message)}
		}
		return "", "", 0, errors.New("refresh response contained invalid credentials")
	}
	return nextAccessToken, nextRefreshToken, nextExpiresIn, nil
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
		return Data{}, refreshFailure(response.StatusCode, "NewAPI refresh returned HTTP %d")
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

// refreshFailure classifies a non-2xx refresh response so the collector can
// distinguish credentials that the site rejected (401/403, a real login
// expiry) from transient failures (5xx, gateway errors). Only a credential
// rejection should mark a site login_expired; a transient refresh failure
// must not permanently lock the site out, since the stored refresh cookie
// may still be valid on the next attempt.
func refreshFailure(statusCode int, format string) error {
	message := fmt.Sprintf(format, statusCode)
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return &adapter.FetchError{StatusCode: statusCode, LoginRequired: true, Err: errors.New(message)}
	}
	return errors.New(message)
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
