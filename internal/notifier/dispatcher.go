package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"relayscope/internal/store"
)

// Dispatcher polls the notification outbox and sends messages to external
// platforms with per-platform rate limiting and exponential backoff.
type Dispatcher struct {
	store        *store.Store
	logger       *slog.Logger
	siteURL      string
	senders      map[string]Sender
	limiters     map[string]*tokenBucket
	pollInterval time.Duration
	stop         chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
}

// Sender sends a notification to one external platform.
type Sender interface {
	Platform() string
	Send(ctx context.Context, target string, msg Message) error
}

// Message is the rendered notification payload.
type Message struct {
	Title string
	Body  string
	URL   string // optional deep link
}

// Config holds dispatcher configuration.
type Config struct {
	Store        *store.Store
	Logger       *slog.Logger
	PollInterval time.Duration
	SiteURL      string // 站点地址，用于续费提醒等面向用户的消息（可为空）
	Telegram     *TelegramConfig
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
	if cfg.Bark != nil {
		// Key 仅作为 target 为空时的回退；测试推送与用户订阅始终自带 target
		s := NewBarkSender(cfg.Bark)
		senders[s.Platform()] = s
		limiters[s.Platform()] = newTokenBucket(5.0, 10) // 5 msg/sec
	}

	return &Dispatcher{
		store:        cfg.Store,
		logger:       cfg.Logger,
		siteURL:      strings.TrimRight(strings.TrimSpace(cfg.SiteURL), "/"),
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
	allowed, err := d.store.NotificationCanSend(ctx, entry.ID)
	if err != nil {
		d.logger.Error("check notification subscription failed", "id", entry.ID, "error", err)
		return
	}
	if !allowed {
		return
	}
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
	grants := map[int64]*pushGrant{}
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
			// 非会员免费额度：只推送最早订阅的 3 个站点，超额站点暂停（数据保留，续期自动恢复）
			if !d.pushGrantFor(ctx, sub.UserID, grants).allows(sub.SiteID) {
				continue
			}
			msg := renderMessage(ann)
			payload, _ := json.Marshal(msg)
			if err := d.store.EnqueueNotification(ctx, sub.ID, ann.ID, ann.SiteID, sub.Platform, sub.Target, string(payload), ann.ContentHash, ann.Title); err != nil {
				if !strings.Contains(err.Error(), "UNIQUE") {
					d.logger.Error("enqueue notification failed", "sub_id", sub.ID, "ann_id", ann.ID, "error", err)
				}
			}
			_ = sender // used for validation above
		}
	}
}

// freePushSites 非会员可收到推送的站点数上限，与前端 NOTIFY_FREE_LIMIT / 服务端订阅限额一致。
const freePushSites = 3

// pushGrant 描述一个用户的推送许可：会员全量放行；非会员仅其最早订阅的前
// freePushSites 个站点（按订阅 id 升序去重，与前端「已暂停」徽标同一规则）。
type pushGrant struct {
	all          bool
	allowedSites map[int64]struct{}
}

func (g *pushGrant) allows(siteID int64) bool {
	if g.all {
		return true
	}
	_, ok := g.allowedSites[siteID]
	return ok
}

func (d *Dispatcher) pushGrantFor(ctx context.Context, userID int64, cache map[int64]*pushGrant) *pushGrant {
	if g, ok := cache[userID]; ok {
		return g
	}
	g := &pushGrant{allowedSites: map[int64]struct{}{}}
	membership, err := d.store.GetMembership(ctx, userID)
	switch {
	case err != nil:
		d.logger.Warn("read membership for push grant failed", "user_id", userID, "error", err)
	case membership.Active:
		g.all = true
	default:
		if subs, sErr := d.store.ListUserSubscriptions(ctx, userID); sErr != nil {
			d.logger.Warn("list subscriptions for push grant failed", "user_id", userID, "error", sErr)
		} else {
			sort.Slice(subs, func(left, right int) bool { return subs[left].ID < subs[right].ID })
			seen := map[int64]struct{}{}
			for _, sub := range subs {
				if _, dup := seen[sub.SiteID]; dup {
					continue
				}
				seen[sub.SiteID] = struct{}{}
				if len(g.allowedSites) < freePushSites {
					g.allowedSites[sub.SiteID] = struct{}{}
				}
			}
		}
	}
	cache[userID] = g
	return g
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

// renewalReminderWindow 会员到期前多久开始发第一档续费提醒（提前 3 天）。
const renewalReminderWindow = 72 * time.Hour

// expiryDayReminderGrace 到期当天提醒的补发窗口：错过到期日（如整天宕机）后仍可补发一天。
const expiryDayReminderGrace = 48 * time.Hour

// membershipLocation 判断「到期当天」所用时区：用户均为国内用户，按东八区日历日。
var membershipLocation = time.FixedZone("UTC+8", 8*3600)

// renewalReminderKinds 返回该候选此刻应发的提醒档位。
func renewalReminderKinds(candidate store.RenewalReminderCandidate, now time.Time) []string {
	var kinds []string
	if candidate.ExpiresAt.After(now) {
		kinds = append(kinds, store.RenewalReminderKindExpiring)
	}
	expiryDayStart := dayStartIn(candidate.ExpiresAt, membershipLocation)
	if !now.Before(expiryDayStart) && now.Before(expiryDayStart.Add(expiryDayReminderGrace)) {
		kinds = append(kinds, store.RenewalReminderKindExpiryDay)
	}
	return kinds
}

func dayStartIn(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// StartRenewalReminders 启动会员续费提醒循环：到期前 3 天内发一次、到期当天再发一次。
// 每档按 (user_id, 到期时间, 档位) 先落库占位再发送，同一有效期每档绝不重复发送；
// 全部渠道发送失败则撤销占位，下轮重试。
func (d *Dispatcher) StartRenewalReminders(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.runRenewalReminders(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-d.stop:
				return
			case <-ticker.C:
				d.runRenewalReminders(ctx)
			}
		}
	}()
}

func (d *Dispatcher) runRenewalReminders(ctx context.Context) {
	now := time.Now().UTC()
	// 下界回溯 48h：覆盖到期当天提醒的补发窗口。
	candidates, err := d.store.ListRenewalReminderCandidates(ctx, now.Add(-expiryDayReminderGrace), now.Add(renewalReminderWindow))
	if err != nil {
		d.logger.Warn("list renewal reminder candidates failed", "error", err)
		return
	}
	for _, candidate := range candidates {
		for _, kind := range renewalReminderKinds(candidate, now) {
			d.sendRenewalReminder(ctx, candidate, kind)
		}
	}
}

func (d *Dispatcher) sendRenewalReminder(ctx context.Context, candidate store.RenewalReminderCandidate, kind string) {
	claimed, err := d.store.ClaimRenewalReminder(ctx, candidate.UserID, candidate.ExpiresAt, kind, time.Now().UTC())
	if err != nil {
		d.logger.Warn("claim renewal reminder failed", "user_id", candidate.UserID, "kind", kind, "error", err)
		return
	}
	if !claimed {
		return // 该档此前已提醒过
	}
	subs, err := d.store.ListUserSubscriptions(ctx, candidate.UserID)
	if err != nil {
		d.logger.Warn("list subscriptions for renewal reminder failed", "user_id", candidate.UserID, "error", err)
		d.releaseRenewalReminder(ctx, candidate, kind)
		return
	}
	seen := map[string]struct{}{}
	sentAny := false
	for _, sub := range subs {
		if !sub.Enabled {
			continue
		}
		key := sub.Platform + "|" + sub.Target
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		sender, ok := d.senders[sub.Platform]
		if !ok {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := sender.Send(sendCtx, sub.Target, d.renewalMessage(candidate, kind))
		cancel()
		if err != nil {
			d.logger.Warn("renewal reminder send failed", "user_id", candidate.UserID, "platform", sub.Platform, "error", err)
			continue
		}
		sentAny = true
	}
	if !sentAny {
		// 一条都没发出去：撤销占位，下一轮重试。
		d.releaseRenewalReminder(ctx, candidate, kind)
		return
	}
	d.logger.Info("renewal reminder sent", "user_id", candidate.UserID, "kind", kind, "expires_at", candidate.ExpiresAt.Format("2006-01-02"))
}

func (d *Dispatcher) releaseRenewalReminder(ctx context.Context, candidate store.RenewalReminderCandidate, kind string) {
	if err := d.store.ReleaseRenewalReminder(ctx, candidate.UserID, candidate.ExpiresAt, kind); err != nil {
		d.logger.Error("release renewal reminder failed", "user_id", candidate.UserID, "kind", kind, "error", err)
	}
}

func (d *Dispatcher) renewalMessage(candidate store.RenewalReminderCandidate, kind string) Message {
	var body string
	var title string
	if kind == store.RenewalReminderKindExpiryDay {
		title = "⏳ RelayScope 会员今日到期"
		body = fmt.Sprintf("你的会员今日到期（%s）。到期后定制功能暂停、超出免费额度（3 个站点）的订阅推送将暂停，续费后全部自动恢复。", candidate.ExpiresAt.In(membershipLocation).Format("2006-01-02"))
	} else {
		days := int(time.Until(candidate.ExpiresAt).Hours()/24) + 1
		if days < 0 {
			days = 0
		}
		title = "⏳ RelayScope 会员即将到期"
		body = fmt.Sprintf("你的会员将于 %s 到期（约剩 %d 天）。到期后定制功能暂停、超出免费额度（3 个站点）的订阅推送将暂停，续费后全部自动恢复。", candidate.ExpiresAt.Format("2006-01-02"), days)
	}
	if d.siteURL != "" {
		body += "\n\n续费入口：" + d.siteURL
	}
	return Message{Title: title, Body: body}
}
