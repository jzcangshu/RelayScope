package session

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

var ErrLoginWorkflowUnavailable = errors.New("login workflow executor is unavailable")
var ErrLoginChallengeBlocked = errors.New("login page is blocked by a challenge")

type BrowserPage interface {
	Open(ctx context.Context, rawURL string) error
	VisibleText(ctx context.Context) (string, error)
	ClickLogin(ctx context.Context) error
	AcceptConsent(ctx context.Context) error
	ClickLinuxDoAuthorize(ctx context.Context) error
	ExportSession(ctx context.Context) (Data, error)
}

// LoginExecutor is intentionally narrow. A local Chrome/Playwright runner or
// a future server-side browser can implement it without leaking browser APIs
// into the collector and without allowing arbitrary navigation.
type LoginExecutor interface {
	Run(ctx context.Context, siteURL string) (Data, error)
}

type Workflow struct {
	Executor     LoginExecutor
	BrowserLock  sync.Locker
	AllowedHosts map[string]struct{}
	Timeout      time.Duration
}

func (workflow Workflow) Recover(ctx context.Context, siteURL string) (Data, error) {
	if workflow.Executor == nil {
		return Data{}, ErrLoginWorkflowUnavailable
	}
	if !workflow.Allowed(siteURL) {
		return Data{}, fmt.Errorf("site is not allowlisted: %s", redactURL(siteURL))
	}
	if workflow.BrowserLock != nil {
		workflow.BrowserLock.Lock()
		defer workflow.BrowserLock.Unlock()
	}
	timeout := workflow.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	data, err := workflow.Executor.Run(child, siteURL)
	if err != nil {
		return Data{}, err
	}
	if len(data.Cookies) == 0 {
		return Data{}, errors.New("login workflow returned no cookies")
	}
	return data, nil
}

func (workflow Workflow) Allowed(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if _, ok := workflow.AllowedHosts[host]; !ok {
		return false
	}
	return true
}

func redactURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "invalid-url"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// ScriptSignals centralizes the safe, text-only decisions mirrored from the
// supplied userscript. It never clicks a page itself and treats a challenge
// as a hard stop for the login workflow.
type ScriptSignals struct{}

func (ScriptSignals) HasChallenge(text string) bool {
	value := strings.ToLower(text)
	for _, marker := range []string{"just a moment", "checking your browser", "verify you are human", "人机验证", "安全验证", "请完成验证", "turnstile", "cloudflare"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func (ScriptSignals) HasLinuxDoLogin(text string) bool {
	value := strings.ToLower(strings.NewReplacer(".", "", "·", "", " ", "", "_", "", "-", "").Replace(text))
	return strings.Contains(value, "使用linuxdo登录") || strings.Contains(value, "使用linuxdo登陆") || strings.Contains(value, "linuxdocontinue")
}

func (ScriptSignals) IsAuthorizationPage(rawURL, text string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && strings.EqualFold(parsed.Hostname(), "connect.linux.do") && strings.HasPrefix(parsed.Path, "/oauth2/") && strings.Contains(strings.ToLower(text), "允许")
}
