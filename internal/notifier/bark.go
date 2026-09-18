package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// BarkConfig holds Bark push configuration.
type BarkConfig struct {
	Key      string // Device key from Bark app
	BaseURL  string // Optional: custom Bark server URL (default: https://api.day.app)
	Client   *http.Client
}

// BarkSender sends notifications via Bark (iOS push).
type BarkSender struct {
	key     string
	baseURL string
	client  *http.Client
}

func NewBarkSender(cfg *BarkConfig) *BarkSender {
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.day.app"
	}
	return &BarkSender{key: cfg.Key, baseURL: baseURL, client: client}
}

func (s *BarkSender) Platform() string { return "bark" }

func (s *BarkSender) Send(ctx context.Context, target string, msg Message) error {
	key := target
	if key == "" {
		key = s.key
	}

	payload := map[string]string{
		"title": msg.Title,
		"body":  msg.Body,
	}
	if msg.URL != "" {
		payload["url"] = msg.URL
	}
	body, _ := json.Marshal(payload)

	endpoint := fmt.Sprintf("%s/%s", s.baseURL, url.PathEscape(key))
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build bark request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("bark send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("bark returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
