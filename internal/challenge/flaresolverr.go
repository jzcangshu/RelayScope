package challenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"relaypulse/internal/adapter"
)

const (
	flareSolverrChallengeTimeout = 180 * time.Second
	flareSolverrClientTimeout    = flareSolverrChallengeTimeout + 10*time.Second
)

type FlareSolverr struct {
	Endpoint     string
	Client       *http.Client
	mu           sync.Mutex
	cooldown     time.Duration
	blockedUntil time.Time
}

type requestPayload struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"`
}

type responsePayload struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		Response  string `json:"response"`
		UserAgent string `json:"userAgent"`
		Cookies   []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	} `json:"solution"`
}

func NewFlareSolverr(endpoint string) (*FlareSolverr, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		return nil, errors.New("FlareSolverr endpoint must be HTTP on loopback")
	}
	if parsed.Port() == "" {
		return nil, errors.New("FlareSolverr endpoint must include a port")
	}
	return &FlareSolverr{Endpoint: strings.TrimRight(endpoint, "/"), Client: &http.Client{Timeout: flareSolverrClientTimeout}, cooldown: 2 * time.Minute}, nil
}

func (solver *FlareSolverr) Solve(ctx context.Context, rawURL string) (adapter.ChallengeResult, error) {
	if solver == nil {
		return adapter.ChallengeResult{}, errors.New("FlareSolverr is not configured")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return adapter.ChallengeResult{}, errors.New("invalid challenge URL")
	}
	solver.mu.Lock()
	defer solver.mu.Unlock()
	if time.Now().Before(solver.blockedUntil) {
		return adapter.ChallengeResult{}, errors.New("FlareSolverr is in cooldown")
	}
	payload, _ := json.Marshal(requestPayload{Cmd: "request.get", URL: rawURL, MaxTimeout: int(flareSolverrChallengeTimeout / time.Millisecond)})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, solver.Endpoint+"/v1", strings.NewReader(string(payload)))
	if err != nil {
		return adapter.ChallengeResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := solver.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		solver.blockedUntil = time.Now().Add(solver.cooldown)
		return adapter.ChallengeResult{}, fmt.Errorf("FlareSolverr request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		solver.blockedUntil = time.Now().Add(solver.cooldown)
		return adapter.ChallengeResult{}, fmt.Errorf("FlareSolverr returned HTTP %d", response.StatusCode)
	}
	var decoded responsePayload
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		solver.blockedUntil = time.Now().Add(solver.cooldown)
		return adapter.ChallengeResult{}, err
	}
	if decoded.Status != "ok" {
		solver.blockedUntil = time.Now().Add(solver.cooldown)
		return adapter.ChallengeResult{}, fmt.Errorf("FlareSolverr failed: %s", decoded.Message)
	}
	result := adapter.ChallengeResult{UserAgent: decoded.Solution.UserAgent, Body: []byte(decoded.Solution.Response)}
	for _, cookie := range decoded.Solution.Cookies {
		result.Cookies = append(result.Cookies, adapter.ChallengeCookie{Name: cookie.Name, Value: cookie.Value})
	}
	solver.blockedUntil = time.Time{}
	return result, nil
}
