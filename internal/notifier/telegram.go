package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// TelegramConfig holds Telegram Bot API configuration.
type TelegramConfig struct {
	Token  string // Bot token from @BotFather
	Client *http.Client
}

// TelegramSender sends notifications via Telegram Bot API.
type TelegramSender struct {
	token  string
	client *http.Client
}

func NewTelegramSender(cfg *TelegramConfig) *TelegramSender {
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &TelegramSender{token: cfg.Token, client: client}
}

func (s *TelegramSender) Platform() string { return "telegram" }

func (s *TelegramSender) Send(ctx context.Context, target string, msg Message) error {
	text := msg.Title
	if msg.Body != "" {
		text += "\n\n" + msg.Body
	}
	if msg.URL != "" {
		text += "\n\n" + msg.URL
	}
	// Truncate to Telegram's 4096 char limit
	if len(text) > 4096 {
		text = text[:4093] + "..."
	}

	payload := map[string]any{
		"chat_id":    target,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", s.token)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("telegram returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// EscapeMarkdownV2 escapes special characters for Telegram MarkdownV2.
func EscapeMarkdownV2(text string) string {
	special := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	for _, c := range special {
		text = strings.ReplaceAll(text, c, "\\"+c)
	}
	return text
}
