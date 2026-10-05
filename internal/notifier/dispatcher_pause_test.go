package notifier

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"relayscope/internal/store"
)

// 非会员超额站点推送暂停：免费额度内（最早 3 个站点）照常推送，其余暂停；会员全量推送。
func TestEnqueuePausesExcessSitesForNonMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupUserWithSites := func(name string, siteCount int, member bool) (int64, []int64) {
		user, err := db.UpsertUser(ctx, "linuxdo", name, name, name, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		if member {
			if _, err := db.ExtendMembership(ctx, user.ID, 30); err != nil {
				t.Fatal(err)
			}
		}
		var siteIDs []int64
		for i := range siteCount {
			site, err := db.CreateSite(ctx, store.Site{
				Name:       name + "-站" + string(rune('A'+i)),
				BaseURL:    "https://" + name + "-" + string(rune('a'+i)) + ".example.invalid",
				SourceURL:  "https://" + name + "-" + string(rune('a'+i)) + ".example.invalid/s",
				AdapterKey: "probe",
				Interval:   5 * time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			siteIDs = append(siteIDs, site.ID)
			// 首批公告 = 静默回填（避免轰炸语义），只为让后续公告可判新
			if _, err := db.ApplyAnnouncements(ctx, site.ID, []store.AnnouncementInput{
				{ExternalID: name + "-hist", Title: "历史", PublishedAt: time.Now().UTC().Add(-time.Hour)},
			}, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.CreateSubscription(ctx, user.ID, site.ID, "bark", "deviceKey-"+name, "{}"); err != nil {
				t.Fatal(err)
			}
		}
		return user.ID, siteIDs
	}

	freeUserID, freeSites := setupUserWithSites("freebie", 5, false)
	memberUserID, memberSites := setupUserWithSites("member", 5, true)

	d := NewDispatcher(Config{
		Store:  db,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Bark:   &BarkConfig{}, // bark 无需服务端凭据，注册后订阅才可能入队
	})

	// 每个站各产生一条「新公告」
	var news []store.SiteAnnouncement
	now := time.Now().UTC()
	for _, siteID := range append(append([]int64{}, freeSites...), memberSites...) {
		newAnns, err := db.ApplyAnnouncements(ctx, siteID, []store.AnnouncementInput{
			{ExternalID: "fresh", Title: "新公告", PublishedAt: now},
		}, now)
		if err != nil || len(newAnns) != 1 {
			t.Fatalf("apply announcements site %d: %v (news=%d)", siteID, err, len(newAnns))
		}
		news = append(news, newAnns...)
	}

	d.Enqueue(news)

	// 非会员：只有最早 3 个站点进 outbox；会员：5 个全进
	countOutbox := func(userID int64) int {
		subs, err := db.ListUserSubscriptions(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, sub := range subs {
			entries, err := db.ListPendingNotifications(ctx, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.SubscriptionID == sub.ID {
					n++
				}
			}
		}
		return n
	}
	if got := countOutbox(freeUserID); got != freePushSites {
		t.Fatalf("free user outbox entries = %d, want %d (excess sites must be paused)", got, freePushSites)
	}
	if got := countOutbox(memberUserID); got != len(memberSites) {
		t.Fatalf("member outbox entries = %d, want %d", got, len(memberSites))
	}
}

// 续费提醒：到期前 3 天内的订阅用户成为候选；按 (user, expires_at, kind) 占位去重。
func TestRenewalReminderCandidatesAndDedup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()

	mkUser := func(name string) store.User {
		u, err := db.UpsertUser(ctx, "linuxdo", name, name, name, "", 2)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	soon := mkUser("soon")       // 2 天后到期，有订阅 → 候选
	noSub := mkUser("nosub")     // 2 天后到期，无订阅 → 不提醒
	farAway := mkUser("faraway") // 10 天后到期 → 未到提醒窗口
	if _, err := db.ExtendMembership(ctx, soon.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExtendMembership(ctx, noSub.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExtendMembership(ctx, farAway.ID, 10); err != nil {
		t.Fatal(err)
	}
	site, err := db.CreateSite(ctx, store.Site{
		Name: "提醒测试站", BaseURL: "https://reminder.example.invalid",
		SourceURL: "https://reminder.example.invalid/s", AdapterKey: "probe", Interval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSubscription(ctx, soon.ID, site.ID, "telegram", "1212406777", "{}"); err != nil {
		t.Fatal(err)
	}

	candidates, err := db.ListRenewalReminderCandidates(ctx, now.Add(-expiryDayReminderGrace), now.Add(renewalReminderWindow))
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].UserID != soon.ID {
		t.Fatalf("candidates = %+v, want only user soon", candidates)
	}

	// 占位去重：同一 (user, expires_at, kind) 第二次抢占返回 false；撤销后可再次抢占
	first, err := db.ClaimRenewalReminder(ctx, soon.ID, candidates[0].ExpiresAt, store.RenewalReminderKindExpiring, now)
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v; want true", first, err)
	}
	second, err := db.ClaimRenewalReminder(ctx, soon.ID, candidates[0].ExpiresAt, store.RenewalReminderKindExpiring, now)
	if err != nil || second {
		t.Fatalf("second claim = %v, %v; want false", second, err)
	}
	if err := db.ReleaseRenewalReminder(ctx, soon.ID, candidates[0].ExpiresAt, store.RenewalReminderKindExpiring); err != nil {
		t.Fatal(err)
	}
	third, err := db.ClaimRenewalReminder(ctx, soon.ID, candidates[0].ExpiresAt, store.RenewalReminderKindExpiring, now)
	if err != nil || !third {
		t.Fatalf("claim after release = %v, %v; want true", third, err)
	}
	// 到期当天档与提前档互不干扰
	dayKind, err := db.ClaimRenewalReminder(ctx, soon.ID, candidates[0].ExpiresAt, store.RenewalReminderKindExpiryDay, now)
	if err != nil || !dayKind {
		t.Fatalf("expiry-day claim = %v, %v; want true", dayKind, err)
	}
}

// recordingSender 记录收到的推送，可切换为恒失败。
type recordingSender struct {
	platform string
	fail     bool
	mu       sync.Mutex
	targets  []string
	titles   []string
}

func (s *recordingSender) Platform() string { return s.platform }

func (s *recordingSender) Send(_ context.Context, target string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("send failed")
	}
	s.targets = append(s.targets, target)
	s.titles = append(s.titles, msg.Title)
	return nil
}

func (s *recordingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.titles)
}

// 回归：到期前 3 天的提醒必须只发一次（此前先发后记去重，导致每小时 tick 重复推送）。
func TestRenewalReminderSendsOncePerKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	user, err := db.UpsertUser(ctx, "linuxdo", "soon", "soon", "soon", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExtendMembership(ctx, user.ID, 2); err != nil {
		t.Fatal(err)
	}
	site, err := db.CreateSite(ctx, store.Site{
		Name: "提醒测试站", BaseURL: "https://reminder.example.invalid",
		SourceURL: "https://reminder.example.invalid/s", AdapterKey: "probe", Interval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSubscription(ctx, user.ID, site.ID, "telegram", "1212406777", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSubscription(ctx, user.ID, site.ID, "bark", "devicekey", "{}"); err != nil {
		t.Fatal(err)
	}

	rec := &recordingSender{platform: "telegram"}
	d := NewDispatcher(Config{Store: db, Logger: testLogger()})
	d.senders["telegram"] = rec
	d.senders["bark"] = &recordingSender{platform: "bark"}

	// 三轮 tick（模拟此前每小时重复触发的场景）——提前档只发一次，发到全部渠道
	for i := 0; i < 3; i++ {
		d.runRenewalReminders(ctx)
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("telegram renewal reminders sent = %d, want 1", got)
	}
	if rec.titles[0] != "⏳ RelayScope 会员即将到期" {
		t.Fatalf("title = %q, want 即将到期", rec.titles[0])
	}
}

// 到期当天档：到期时间已过但仍在当天/补发窗口内 → 发一次「今日到期」，再次 tick 不重发。
func TestRenewalReminderExpiryDayKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	user, err := db.UpsertUser(ctx, "linuxdo", "today", "today", "today", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	// 直接把到期时间设为 1 小时前（今日已到期）
	expired := time.Now().UTC().Add(-time.Hour)
	if _, err := db.SetMembershipByUsername(ctx, "linuxdo", "today", &expired); err != nil {
		t.Fatal(err)
	}
	site, err := db.CreateSite(ctx, store.Site{
		Name: "提醒测试站", BaseURL: "https://reminder.example.invalid",
		SourceURL: "https://reminder.example.invalid/s", AdapterKey: "probe", Interval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSubscription(ctx, user.ID, site.ID, "telegram", "1212406777", "{}"); err != nil {
		t.Fatal(err)
	}

	rec := &recordingSender{platform: "telegram"}
	d := NewDispatcher(Config{Store: db, Logger: testLogger()})
	d.senders["telegram"] = rec

	for i := 0; i < 2; i++ {
		d.runRenewalReminders(ctx)
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("expiry-day reminders sent = %d, want 1", got)
	}
	if rec.titles[0] != "⏳ RelayScope 会员今日到期" {
		t.Fatalf("title = %q, want 今日到期", rec.titles[0])
	}
}

// 全部渠道发送失败：撤销占位，下一轮重试成功。
func TestRenewalReminderRetriesAfterTotalFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	user, err := db.UpsertUser(ctx, "linuxdo", "soon", "soon", "soon", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExtendMembership(ctx, user.ID, 2); err != nil {
		t.Fatal(err)
	}
	site, err := db.CreateSite(ctx, store.Site{
		Name: "提醒测试站", BaseURL: "https://reminder.example.invalid",
		SourceURL: "https://reminder.example.invalid/s", AdapterKey: "probe", Interval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSubscription(ctx, user.ID, site.ID, "telegram", "1212406777", "{}"); err != nil {
		t.Fatal(err)
	}

	rec := &recordingSender{platform: "telegram", fail: true}
	d := NewDispatcher(Config{Store: db, Logger: testLogger()})
	d.senders["telegram"] = rec

	d.runRenewalReminders(ctx)
	if got := rec.count(); got != 0 {
		t.Fatalf("failed sends recorded = %d, want 0", got)
	}
	rec.mu.Lock()
	rec.fail = false
	rec.mu.Unlock()
	d.runRenewalReminders(ctx)
	if got := rec.count(); got != 1 {
		t.Fatalf("retry after total failure sent = %d, want 1", got)
	}
}

// 档位判定的边界：未到期且在窗口内 → 提前档；已过到期但在补发窗口 → 当天档；过期太久 → 无。
func TestRenewalReminderKinds(t *testing.T) {
	now := time.Now().UTC()
	candidate := func(expires time.Time) store.RenewalReminderCandidate {
		return store.RenewalReminderCandidate{UserID: 1, ExpiresAt: expires}
	}
	if got := renewalReminderKinds(candidate(now.Add(48*time.Hour)), now); len(got) != 1 || got[0] != store.RenewalReminderKindExpiring {
		t.Fatalf("kinds(48h ahead) = %v, want [expiring]", got)
	}
	if got := renewalReminderKinds(candidate(now.Add(-time.Hour)), now); len(got) != 1 || got[0] != store.RenewalReminderKindExpiryDay {
		t.Fatalf("kinds(1h overdue) = %v, want [expiry_day]", got)
	}
	if got := renewalReminderKinds(candidate(now.Add(-96*time.Hour)), now); len(got) != 0 {
		t.Fatalf("kinds(96h overdue) = %v, want none", got)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
