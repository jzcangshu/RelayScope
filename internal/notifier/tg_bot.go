package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TelegramBot 提供 getUpdates 长轮询，让用户给 bot 发 /start 或 /chatid 时，
// bot 主动回显他的 Chat ID，省去手动翻 getUpdates JSON 的麻烦。
// 仅在 TelegramConfig 配置（RELAYSCOPE_TELEGRAM_TOKEN 非空）时由 main 启动。
// 注意：getUpdates 与 setWebhook 互斥，启用本功能时不能同时给 bot 设 webhook。
type TelegramBot struct {
	token       string
	siteURL     string // 站点地址，写进引导文案让 bot 自洽（可为空）
	client      *http.Client
	logger      *slog.Logger
	bottomToken int64 // 已确认消费到的 update_id，用于 offset 续传（confirmation）
	mu          sync.Mutex
	stop        chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

func NewTelegramBot(cfg *TelegramConfig, siteURL string, logger *slog.Logger) *TelegramBot {
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TelegramBot{token: cfg.Token, siteURL: strings.TrimRight(strings.TrimSpace(siteURL), "/"), client: client, logger: logger, stop: make(chan struct{})}
}

// Start 在后台启动长轮询循环；ctx 取消或调用 Stop 后退出。
func (b *TelegramBot) Start(ctx context.Context) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.logger.Info("telegram bot listener started")
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.stop:
				return
			default:
			}
			if err := b.pollOnce(ctx); err != nil {
				if ctx.Err() != nil || b.isStopped() {
					return
				}
				b.logger.Warn("telegram bot poll error", "error", err)
				select {
				case <-ctx.Done():
					return
				case <-b.stop:
					return
				case <-time.After(2 * time.Second):
				}
			}
		}
	}()
}

func (b *TelegramBot) Stop() {
	b.stopOnce.Do(func() { close(b.stop) })
	b.wg.Wait()
}

func (b *TelegramBot) isStopped() bool {
	select {
	case <-b.stop:
		return true
	default:
		return false
	}
}

// nextOffset 返回当前消费到的 update_id + 1（confirmation）。
func (b *TelegramBot) nextOffset() (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bottomToken == 0 {
		return 0, false
	}
	return b.bottomToken + 1, true
}

func (b *TelegramBot) confirm(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id > b.bottomToken {
		b.bottomToken = id
	}
}

func (b *TelegramBot) apiURL(method string) string {
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.token, method)
}

// pollOnce 拉取一次 getUpdates 并处理消息。
func (b *TelegramBot) pollOnce(ctx context.Context) error {
	offset, hasOffset := b.nextOffset()
	reqBody := map[string]any{
		"timeout":         30,
		"allowed_updates": []string{"message"},
	}
	if hasOffset {
		reqBody["offset"] = offset
	}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", b.apiURL("getUpdates"), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build getUpdates: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("getUpdates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("getUpdates HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var result struct {
		OK     bool `json:"ok"`
		Result []struct {
			UpdateID int64 `json:"update_id"`
			Message  *struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
				Text string `json:"text"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode getUpdates: %w", err)
	}
	for _, upd := range result.Result {
		b.confirm(upd.UpdateID)
		m := upd.Message
		if m == nil {
			continue
		}
		b.handleMessage(ctx, m.Chat.ID, m.Text)
	}
	return nil
}

func (b *TelegramBot) handleMessage(ctx context.Context, chatID int64, text string) {
	if chatIDIntent(text) {
		b.sendReply(ctx, map[string]any{"chat_id": chatID, "text": chatIDReply(chatID, b.siteURL)})
		b.logger.Info("telegram bot replied chat id", "chat_id", chatID)
		return
	}
	// 小白兜底：没按 START 直接打字的用户，给一句最短指引（无文字的消息如贴图不回）
	if strings.TrimSpace(text) != "" {
		b.sendReply(ctx, map[string]any{"chat_id": chatID, "text": unknownTextReply()})
	}
}

// chatIDReply 生成 Chat ID 回显 + 后续三步引导（bot 端承载详细动线，网页只放简版）。
func chatIDReply(chatID int64, siteURL string) string {
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	var sb strings.Builder
	fmt.Fprintf(&sb, "✅ 你的 Chat ID：%d\n\n", chatID)
	sb.WriteString("完成订阅还需 3 步：\n")
	sb.WriteString("1️⃣ 复制上面这串数字\n")
	if siteURL != "" {
		fmt.Fprintf(&sb, "2️⃣ 打开 %s → 定制 → 通知订阅，粘贴进「推送目标」\n", siteURL)
	} else {
		sb.WriteString("2️⃣ 打开 RelayScope → 定制 → 通知订阅，粘贴进「推送目标」\n")
	}
	sb.WriteString("3️⃣ 点「测试推送」，手机收到后点「保存渠道」\n\n")
	sb.WriteString("然后勾选想关注的站点，公告更新会第一时间推送到这里。")
	return sb.String()
}

// unknownTextReply 对非命令消息回一句最短指引。
func unknownTextReply() string {
	return "发送 /chatid 获取你的 Chat ID，用于在 RelayScope 订阅站点公告推送。"
}

// chatIDIntent 判断消息是否请求查询本人 Chat ID（/start、/chatid 及常见别名）。
func chatIDIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "/start" || lower == "/chatid" || lower == "chatid" || lower == "chat id" || lower == "chatid?" {
		return true
	}
	return strings.HasPrefix(lower, "/start ") || strings.HasPrefix(lower, "/chatid")
}

func (b *TelegramBot) sendReply(ctx context.Context, payload map[string]any) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", b.apiURL("sendMessage"), bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		b.logger.Warn("telegram bot reply failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		b.logger.Warn("telegram bot reply HTTP error", "code", resp.StatusCode, "body", string(raw))
	}
}
