package notifier

import (
	"context"
	"io"
	"log/slog"
	"relayscope/internal/store"
	"strings"
	"testing"
	"time"
)

func TestAnnouncementEditsDoNotReplayPreviousPushes(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	site, err := db.CreateSite(ctx, store.Site{Name: "test", BaseURL: "https://example.invalid", SourceURL: "https://example.invalid/s", AdapterKey: "probe", Interval: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.UpsertUser(ctx, "linuxdo", "edits", "edits", "edits", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := db.CreateSubscription(ctx, user.ID, site.ID, "bark", "test", "{}")
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{platform: "bark"}
	d := NewDispatcher(Config{Store: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	d.senders["bark"] = sender
	apply := func(input store.AnnouncementInput, want int) {
		t.Helper()
		news, err := db.ApplyAnnouncements(ctx, site.ID, []store.AnnouncementInput{input}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		d.Enqueue(news)
		d.Enqueue(news) // callbacks may be retried before dispatch
		d.dispatch(ctx)
		d.Enqueue(news) // and after a successful dispatch
		d.dispatch(ctx)
		if got := sender.count(); got != want {
			t.Fatalf("send count=%d want=%d", got, want)
		}
	}
	baseline := store.AnnouncementInput{ExternalID: "same", Title: "baseline", Content: "old"}
	apply(baseline, 0)
	first := baseline
	first.Content = "new"
	apply(first, 1)
	apply(first, 1)
	second := first
	second.Title = "edited title"
	apply(second, 2)
	apply(first, 2)
	apply(baseline, 2)
	apply(second, 2)
	long := second
	long.Content = strings.Repeat("长", 2001) + "A"
	apply(long, 3)
	long.Content = strings.Repeat("长", 2001) + "B"
	apply(long, 4)
	long.Content += "cancel me"
	news, err := db.ApplyAnnouncements(ctx, site.ID, []store.AnnouncementInput{long}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d.Enqueue(news)
	pending, err := db.ListPendingNotifications(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if err := db.DeleteSubscription(ctx, user.ID+1, sub.ID); err == nil {
		t.Fatal("wrong owner deleted subscription")
	}
	if allowed, err := db.NotificationCanSend(ctx, pending[0].ID); err != nil || !allowed {
		t.Fatalf("wrong owner cancelled: %v %v", allowed, err)
	}
	if err := db.DeleteSubscription(ctx, user.ID, sub.ID); err != nil {
		t.Fatal(err)
	}
	if items, err := db.ListPendingNotifications(ctx, 10); err != nil || len(items) != 0 {
		t.Fatalf("cancelled pending=%v err=%v", items, err)
	}
	d.sendOne(ctx, sender, pending[0]) // cancellation after the batch was fetched
	if sender.count() != 4 {
		t.Fatal("sent a cancelled message")
	}
	newSub, err := db.CreateSubscription(ctx, user.ID, site.ID, "bark", "test", "{}")
	if err != nil || newSub.ID <= sub.ID {
		t.Fatalf("reused deleted subscription: %+v %v", newSub, err)
	}
	d.sendOne(ctx, sender, pending[0])
	if sender.count() != 4 {
		t.Fatal("new subscription revived old message")
	}
}
