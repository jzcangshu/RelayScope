package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"relayscope/internal/store"
)

// Dispatcher polls the notification outbox and sends messages to external
// platforms with per-platform rate limiting and exponential backoff.
type Dispatcher struct {
	store      *store.Store
	logger     *slog.Logger
	senders    map[string]Sender
	limiters   map[string]*tokenBucket
	pollInterval time.Duration
	stop       chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
}

// Sender sends a notification to one external platform.
type Sender interface {
	Platform() string
	Send(ctx context.Context, target string, msg Message) error
}

// Message is the rendered notification payload.
type Message struct {
	Title   string
	Body    string
	URL     string // optional deep link
}

// Config holds dispatcher configuration.
type Config struct {
	Store        *store.Store
	Logger       *slog.Logger
	PollInterval time.Duration
	Telegram     *TelegramConfig
	Feishu       *FeishuConfig
	Bark         *BarkConfig
}

// NewDispatcher creates a new notification dispatcher.
func NewDispatcher(cfg Config) *Dispatcher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	senders := make(map[string]Sender)
	limiters := make(map[string]*tokenBucket)

	if cfg.Telegram != nil && cfg.Telegram.Token != "" {
		s := NewTelegramSender(cfg.Telegram)
		senders[s.Platform()] = s
		limiters[s.Platform()] = newTokenBucket(1.0, 1) // 1 msg/sec
	}
	if cfg.Feishu != nil && cfg.Feishu.WebhookURL != "" {
		s := NewFeishuSender(cfg.Feishu)
		senders[s.Platform()] = s
		limiters[s.Platform()] = newTokenBucket(5.0, 10) // 5 msg/sec
	}
	if cfg.Bark != nil && cfg.Bark.Key != "" {
		s := NewBarkSender(cfg.Bark)
		senders[s.Platform()] = s
		limiters[s.Platform()] = newTokenBucket(5.0, 10) // 5 msg/sec
	}

	return &Dispatcher{
		store:        cfg.Store,
		logger:       cfg.Logger,
		senders:      senders,
		limiters:     limiters,
		pollInterval: cfg.PollInterval,
		stop:         make(chan struct{}),
	}
}

// Start begins the dispatcher loop.
func (d *Dispatcher) Start(ctx context.Context) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(d.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.dispatch(ctx)
			case <-ctx.Done():
				return
			case <-d.stop:
				return
			}
		}
	}()
}

// Stop stops the dispatcher.
func (d *Dispatcher) Stop() {
	d.stopOnce.Do(func() { close(d.stop) })
	d.wg.Wait()
}

// SenderCount returns the number of registered senders (for diagnostics).
func (d *Dispatcher) SenderCount() int { return len(d.senders) }

// Senders exposes the configured platform senders so the HTTP layer can
// reuse them for user-initiated test pushes.
func (d *Dispatcher) Senders() map[string]Sender { return d.senders }

func (d *Dispatcher) dispatch(ctx context.Context) {
	entries, err := d.store.ListPendingNotifications(ctx, 50)
	if err != nil {
		d.logger.Error("list pending notifications failed", "error", err)
		return
	}
	if len(entries) == 0 {
		return
	}
	// Group by platform for rate limiting
	byPlatform := make(map[string][]store.NotificationOutboxEntry)
	for _, entry := range entries {
		byPlatform[entry.Platform] = append(byPlatform[entry.Platform], entry)
	}
	for platform, platformEntries := range byPlatform {
		sender, ok := d.senders[platform]
		if !ok {
			d.logger.Warn("no sender for platform, marking failed", "platform", platform)
			for _, entry := range platformEntries {
				d.markFailed(ctx, entry.ID, "no sender configured for platform "+platform)
			}
			continue
		}
		limiter, _ := d.limiters[platform]
		for _, entry := range platformEntries {
			if limiter != nil {
				limiter.wait(ctx)
			}
			d.sendOne(ctx, sender, entry)
		}
	}
}

func (d *Dispatcher) sendOne(ctx context.Context, sender Sender, entry store.NotificationOutboxEntry) {
	var msg Message
	if err := json.Unmarshal([]byte(entry.Payload), &msg); err != nil {
		d.markFailed(ctx, entry.ID, "invalid payload: "+err.Error())
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := sender.Send(sendCtx, entry.Target, msg); err != nil {
		d.logger.Warn("notification send failed", "platform", entry.Platform, "target", entry.Target, "error", err)
		d.markRetry(ctx, entry)
		return
	}
	if err := d.store.MarkNotificationSent(ctx, entry.ID); err != nil {
		d.logger.Error("mark notification sent failed", "id", entry.ID, "error", err)
	}
	// Record delivery
	_ = d.store.RecordDelivery(ctx, entry.SubscriptionID, entry.AnnouncementID, entry.SiteID, entry.Platform, "delivered", "")
}

func (d *Dispatcher) markFailed(ctx context.Context, id int64, message string) {
	if err := d.store.MarkNotificationFailed(ctx, id); err != nil {
		d.logger.Error("mark notification failed failed", "id", id, "error", err)
	}
}

func (d *Dispatcher) markRetry(ctx context.Context, entry store.NotificationOutboxEntry) {
	backoff := retryBackoff(entry.RetryCount)
	if entry.RetryCount >= 5 {
		d.markFailed(ctx, entry.ID, "max retries exceeded")
		_ = d.store.RecordDelivery(context.Background(), entry.SubscriptionID, entry.AnnouncementID, entry.SiteID, entry.Platform, "failed", "max retries exceeded")
		return
	}
	nextRetry := time.Now().UTC().Add(backoff)
	if err := d.store.ScheduleNotificationRetry(ctx, entry.ID, entry.RetryCount+1, nextRetry); err != nil {
		d.logger.Error("schedule retry failed", "id", entry.ID, "error", err)
	}
}

func retryBackoff(retryCount int) time.Duration {
	switch retryCount {
	case 0:
		return 1 * time.Minute
	case 1:
		return 5 * time.Minute
	case 2:
		return 15 * time.Minute
	case 3:
		return 1 * time.Hour
	default:
		return 4 * time.Hour
	}
}

// Enqueue adds announcements to the notification outbox for all active subscriptions.
func (d *Dispatcher) Enqueue(announcements []store.SiteAnnouncement) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, ann := range announcements {
		subs, err := d.store.ListActiveSubscriptionsForSite(ctx, ann.SiteID)
		if err != nil {
			d.logger.Error("list subscriptions for site failed", "site_id", ann.SiteID, "error", err)
			continue
		}
		for _, sub := range subs {
			sender, ok := d.senders[sub.Platform]
			if !ok {
				continue
			}
			msg := renderMessage(ann)
			payload, _ := json.Marshal(msg)
			if err := d.store.EnqueueNotification(ctx, sub.ID, ann.ID, ann.SiteID, sub.Platform, sub.Target, string(payload)); err != nil {
				if !strings.Contains(err.Error(), "UNIQUE") {
					d.logger.Error("enqueue notification failed", "sub_id", sub.ID, "ann_id", ann.ID, "error", err)
				}
			}
			_ = sender // used for validation above
		}
	}
}

func renderMessage(ann store.SiteAnnouncement) Message {
	title := fmt.Sprintf("📢 %s", ann.SiteName)
	if ann.Title != "" {
		title = fmt.Sprintf("📢 %s | %s", ann.SiteName, ann.Title)
	}
	body := ann.Content
	if len([]rune(body)) > 2000 {
		body = string([]rune(body)[:2000]) + "..."
	}
	return Message{
		Title: title,
		Body:  body,
	}
}
