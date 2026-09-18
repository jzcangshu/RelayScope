package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// FeishuConfig holds Feishu webhook configuration.
type FeishuConfig struct {
	WebhookURL string            // Full webhook URL: https://open.feishu.cn/open-apis/bot/v2/hook/{token}
	Secret     string            // Optional: HMAC-SHA256 sign secret
	Headers    map[string]string // Optional extra headers
	Client     *http.Client
}

// FeishuSender sends notifications via Feishu/Lark webhook.
type FeishuSender struct {
	webhookURL string
	secret     string
	headers    map[string]string
	client     *http.Client
}

func NewFeishuSender(cfg *FeishuConfig) *FeishuSender {
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &FeishuSender{
		webhookURL: cfg.WebhookURL,
		secret:     cfg.Secret,
		headers:    cfg.Headers,
		client:     client,
	}
}

func (s *FeishuSender) Platform() string { return "feishu" }

func (s *FeishuSender) Send(ctx context.Context, target string, msg Message) error {
	// Build an interactive card message for rich rendering
	card := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header": map[string]any{
				"title": map[string]any{
					"tag":     "plain_text",
					"content": msg.Title,
				},
				"template": "blue",
			},
			"elements": []any{
				map[string]any{
					"tag":     "markdown",
					"content": msg.Body,
				},
			},
		},
	}
	body, _ := json.Marshal(card)

	webhookURL := target
	if webhookURL == "" {
		webhookURL = s.webhookURL
	}

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build feishu request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("feishu send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("feishu returned %d: %s", resp.StatusCode, string(respBody))
	}
	// Check response code in body
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Code != 0 {
		return fmt.Errorf("feishu error %d: %s", result.Code, result.Msg)
	}
	return nil
}
