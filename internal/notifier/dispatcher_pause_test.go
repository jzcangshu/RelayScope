package notifier

import (
	"context"
	"io"
	"log/slog"
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

// 续费提醒：到期前 3 天内的订阅用户成为候选；按 (user, expires_at) 去重。
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

	candidates, err := db.ListRenewalReminderCandidates(ctx, now.Add(renewalReminderWindow))
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].UserID != soon.ID {
		t.Fatalf("candidates = %+v, want only user soon", candidates)
	}

	// 去重：同一 (user, expires_at) 第二次标记返回 false
	first, err := db.MarkRenewalReminderSent(ctx, soon.ID, candidates[0].ExpiresAt, now)
	if err != nil || !first {
		t.Fatalf("first mark = %v, %v; want true", first, err)
	}
	second, err := db.MarkRenewalReminderSent(ctx, soon.ID, candidates[0].ExpiresAt, now)
	if err != nil || second {
		t.Fatalf("second mark = %v, %v; want false", second, err)
	}
}
