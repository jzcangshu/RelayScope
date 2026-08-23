package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"relaypulse/internal/domain"
)

// FetchError keeps transport state available to the collector without making
// adapters parse human-readable HTTP error strings.
type FetchError struct {
	URL             string
	StatusCode      int
	Challenge       bool
	ChallengeFailed bool
	LoginRequired   bool
	Err             error
}

func (err *FetchError) Error() string {
	if err.Err != nil {
		return err.Err.Error()
	}
	return fmt.Sprintf("fetch returned HTTP %d", err.StatusCode)
}

func (err *FetchError) Unwrap() error { return err.Err }

type Site struct {
	ID              int64
	Name            string
	BaseURL         string
	SourceURL       string
	ConfigJSON      string
	SessionRequired bool
}

type Fetcher interface {
	GetJSON(ctx context.Context, rawURL string, target any) error
	GetBytes(ctx context.Context, rawURL string) ([]byte, http.Header, error)
}

// SiteFetcher allows the collector to derive a short-lived, site-scoped
// client (for example one carrying an encrypted LinuxDo session).
type SiteFetcher interface {
	FetcherForSite(ctx context.Context, site Site) (Fetcher, error)
}

type ChallengeCookie struct {
	Name  string
	Value string
}

type ChallengeResult struct {
	UserAgent string
	Cookies   []ChallengeCookie
	Body      []byte
}

type ChallengeProvider interface {
	Solve(ctx context.Context, rawURL string) (ChallengeResult, error)
}

type Adapter interface {
	Key() string
	DisplayName() string
	ConfigSchema() json.RawMessage
	Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error)
}

type DetailCollector interface {
	CollectDetails(ctx context.Context, site Site, fetcher Fetcher, collection *domain.Collection, modelNames []string, now time.Time) error
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	registry := &Registry{adapters: make(map[string]Adapter, len(adapters))}
	for _, adapter := range adapters {
		if adapter == nil || strings.TrimSpace(adapter.Key()) == "" {
			return nil, errors.New("adapter and adapter key are required")
		}
		if _, exists := registry.adapters[adapter.Key()]; exists {
			return nil, fmt.Errorf("duplicate adapter key %q", adapter.Key())
		}
		registry.adapters[adapter.Key()] = adapter
	}
	return registry, nil
}

func (registry *Registry) Get(key string) (Adapter, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	adapter, ok := registry.adapters[key]
	return adapter, ok
}

func (registry *Registry) List() []Adapter {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]Adapter, 0, len(registry.adapters))
	for _, adapter := range registry.adapters {
		result = append(result, adapter)
	}
	return result
}

type HTTPFetcher struct {
	Client    *http.Client
	UserAgent string
	MaxBytes  int64
	Challenge ChallengeProvider
	Cookies   []ChallengeCookie
	Headers   map[string]string
}

func (fetcher HTTPFetcher) FetcherForSite(context.Context, Site) (Fetcher, error) {
	return fetcher, nil
}

func (fetcher HTTPFetcher) GetJSON(ctx context.Context, rawURL string, target any) error {
	body, _, err := fetcher.GetBytes(ctx, rawURL)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode JSON from %s: %w", rawURL, err)
	}
	return nil
}

func (fetcher HTTPFetcher) GetBytes(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return nil, nil, fmt.Errorf("invalid fetch URL: %w", err)
	}
	client := fetcher.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build fetch request: %w", err)
	}
	if fetcher.UserAgent != "" {
		request.Header.Set("User-Agent", fetcher.UserAgent)
	}
	setRequestHeaders(request, fetcher.Headers)
	if len(fetcher.Cookies) > 0 {
		values := make([]string, 0, len(fetcher.Cookies))
		for _, cookie := range fetcher.Cookies {
			if cookie.Name != "" {
				values = append(values, cookie.Name+"="+cookie.Value)
			}
		}
		request.Header.Set("Cookie", strings.Join(values, "; "))
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		preview, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		challenge := looksLikeChallenge(response.Header.Get("Content-Type"), string(preview))
		if challenge && fetcher.Challenge != nil {
			solved, solveErr := fetcher.Challenge.Solve(ctx, rawURL)
			if solveErr != nil {
				return nil, response.Header, &FetchError{URL: rawURL, StatusCode: response.StatusCode, Challenge: true, ChallengeFailed: true, Err: fmt.Errorf("challenge solve failed: %w", solveErr)}
			}
			retry, retryErr := fetcher.requestWithChallenge(ctx, rawURL, solved)
			if retryErr != nil {
				return nil, response.Header, retryErr
			}
			return retry, response.Header, nil
		}
		return nil, response.Header, &FetchError{
			URL: rawURL, StatusCode: response.StatusCode,
			Challenge:     challenge,
			LoginRequired: response.StatusCode == http.StatusUnauthorized,
			Err:           fmt.Errorf("fetch returned HTTP %d", response.StatusCode),
		}
	}
	maxBytes := fetcher.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, response.Header, fmt.Errorf("read %s: %w", rawURL, err)
	}
	if int64(len(body)) > maxBytes {
		return nil, response.Header, fmt.Errorf("response from %s exceeds %d bytes", rawURL, maxBytes)
	}
	return body, response.Header, nil
}

func (fetcher HTTPFetcher) requestWithChallenge(ctx context.Context, rawURL string, solved ChallengeResult) ([]byte, error) {
	client := fetcher.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	userAgent := solved.UserAgent
	if userAgent == "" {
		userAgent = fetcher.UserAgent
	}
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	setRequestHeaders(request, fetcher.Headers)
	mergedCookies := mergeCookies(fetcher.Cookies, solved.Cookies)
	if len(mergedCookies) > 0 {
		cookieValues := make([]string, 0, len(mergedCookies))
		for _, cookie := range mergedCookies {
			if cookie.Name != "" {
				cookieValues = append(cookieValues, cookie.Name+"="+cookie.Value)
			}
		}
		request.Header.Set("Cookie", strings.Join(cookieValues, "; "))
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("retry fetch failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		preview, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		challenge := looksLikeChallenge(response.Header.Get("Content-Type"), string(preview))
		return nil, &FetchError{
			URL: rawURL, StatusCode: response.StatusCode, Challenge: challenge, ChallengeFailed: challenge,
			LoginRequired: response.StatusCode == http.StatusUnauthorized,
			Err:           fmt.Errorf("retry fetch returned HTTP %d", response.StatusCode),
		}
	}
	maxBytes := fetcher.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", rawURL, maxBytes)
	}
	return body, nil
}

func setRequestHeaders(request *http.Request, headers map[string]string) {
	for name, value := range headers {
		if name != "" && value != "" {
			request.Header.Set(name, value)
		}
	}
}

func mergeCookies(base, solved []ChallengeCookie) []ChallengeCookie {
	merged := make([]ChallengeCookie, 0, len(base)+len(solved))
	positions := make(map[string]int, len(base)+len(solved))
	for _, cookie := range base {
		if cookie.Name == "" {
			continue
		}
		positions[cookie.Name] = len(merged)
		merged = append(merged, cookie)
	}
	for _, cookie := range solved {
		if cookie.Name == "" {
			continue
		}
		if index, exists := positions[cookie.Name]; exists {
			merged[index] = cookie
			continue
		}
		positions[cookie.Name] = len(merged)
		merged = append(merged, cookie)
	}
	return merged
}

func looksLikeChallenge(contentType, body string) bool {
	value := strings.ToLower(contentType + " " + body)
	return strings.Contains(value, "cloudflare") || strings.Contains(value, "cf-chl") || strings.Contains(value, "turnstile") || strings.Contains(value, "challenge-platform")
}
