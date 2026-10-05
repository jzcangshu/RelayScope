package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestAnnouncementVersionsStaySilentOnRepeatAndRevert(t *testing.T) {
	db := openTestStore(t)
	site := createTestSite(t, db)
	ctx := context.Background()
	original := AnnouncementInput{ExternalID: "stable", Title: "original", Content: "body"}
	edited := original
	edited.Title = "edited"
	for i, step := range []struct {
		input AnnouncementInput
		want  int
	}{
		{original, 0}, {original, 0}, {edited, 1}, {edited, 0}, {original, 0}, {edited, 0},
	} {
		news, err := db.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{step.input}, time.Now())
		if err != nil || len(news) != step.want {
			t.Fatalf("step %d news=%d want=%d err=%v", i, len(news), step.want, err)
		}
	}
}

func TestAnnouncementVersionMigrationPreservesLegacyQueue(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db := &Store{db: sqlDB}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		version, err := migrationVersion(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if version >= 12 {
			continue
		}
		data, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.Exec(string(data)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sqlDB.Exec(`INSERT INTO app_meta(key,value) VALUES ('schema_version','11') ON CONFLICT(key) DO UPDATE SET value='11'`); err != nil {
		t.Fatal(err)
	}
	if err := db.ensureSiteSchema(ctx); err != nil {
		t.Fatal(err)
	}
	site := createTestSite(t, db)
	if _, err := sqlDB.Exec(`INSERT INTO site_announcements (id,site_id,external_id,title,content,content_hash,published_at,first_seen_at,last_seen_at) VALUES(1,?,'a','stored','body','legacy-hash',1,1,1)`, site.ID); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"sent", "retry", "pending", "failed"} {
		user, err := db.UpsertUser(ctx, "linuxdo", fmt.Sprint(i), fmt.Sprint(i), fmt.Sprint(i), "", 2)
		if err != nil {
			t.Fatal(err)
		}
		sub, err := db.CreateSubscription(ctx, user.ID, site.ID, "bark", "test", "{}")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO notification_outbox(id,subscription_id,announcement_id,site_id,platform,target,payload,status,retry_count,next_retry_at,created_at,sent_at) VALUES(?,?,1,?,'bark','test',?, ?,3,123,10,20)`, i+1, sub.ID, site.ID, `{"Title":"📢 Old | site | Earlier title","Body":"Earlier body","URL":""}`, status); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"sent", "retry", "pending", "failed"} {
		var got string
		var retries, next, created, sent int64
		if err := sqlDB.QueryRow(`SELECT status,retry_count,next_retry_at,created_at,sent_at FROM notification_outbox WHERE id=?`, i+1).Scan(&got, &retries, &next, &created, &sent); err != nil {
			t.Fatal(err)
		}
		if got != status || retries != 3 || next != 123 || created != 10 || sent != 20 {
			t.Fatalf("migration changed row %d: %s %d %d %d %d", i, got, retries, next, created, sent)
		}
		if err := db.EnqueueNotification(ctx, int64(i+1), 1, site.ID, "bark", "test", `{"Title":"📢 Renamed site | Earlier title","Body":"Earlier body","URL":""}`, "earlier-full-version", "Earlier title"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM notification_outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("legacy queue replayed: %d", count)
	}
	for i, payload := range []string{
		`{"Title":"📢 Renamed site | Earlier title","Body":"New body","URL":""}`,
		`{"Title":"📢 Renamed site | New title","Body":"Earlier body","URL":""}`,
	} {
		if err := db.EnqueueNotification(ctx, 1, 1, site.ID, "bark", "test", payload, fmt.Sprintf("new-version-%d", i), []string{"Earlier title", "New title"}[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM notification_outbox`).Scan(&count); err != nil || count != 6 {
		t.Fatalf("legacy compatibility suppressed real edits: count=%d err=%v", count, err)
	}
	input := AnnouncementInput{ExternalID: "a", Title: "stored", Content: "body"}
	news, err := db.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{input}, time.Now())
	if err != nil || len(news) != 0 {
		t.Fatalf("upgrade emitted baseline: %v %v", news, err)
	}
	input.Content = "edited"
	news, err = db.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{input}, time.Now())
	if err != nil || len(news) != 1 {
		t.Fatalf("upgrade suppressed real edit: %v %v", news, err)
	}
}

func TestAnnouncementBackfillDuplicateAndNormalizedMetadata(t *testing.T) {
	db := openTestStore(t)
	site := createTestSite(t, db)
	ctx := context.Background()
	inputs := []AnnouncementInput{{ExternalID: "a", Title: " first ", Content: "body"}, {ExternalID: "a", Title: "second", Content: "body"}}
	if news, err := db.ApplyAnnouncements(ctx, site.ID, inputs, time.Now()); err != nil || len(news) != 0 {
		t.Fatalf("backfill = %v %v", news, err)
	}
	inputs[0].Title = "first"
	inputs[0].AnnType = "default"
	inputs[0].Extra = " "
	if news, err := db.ApplyAnnouncements(ctx, site.ID, inputs[:1], time.Now()); err != nil || len(news) != 0 {
		t.Fatalf("normalized historical revert = %v %v", news, err)
	}
}

func TestNotificationDistinctVersionsAndRetryDeduplication(t *testing.T) {
	db := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, db)
	user, err := db.UpsertUser(ctx, "linuxdo", "versions", "versions", "versions", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := db.CreateSubscription(ctx, user.ID, site.ID, "bark", "test", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{{ExternalID: "a", Content: "baseline"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	anns, err := db.ListSiteAnnouncements(ctx, site.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(payload string) {
		t.Helper()
		if err := db.EnqueueNotification(ctx, sub.ID, anns[0].ID, site.ID, "bark", "test", payload, contentHash("", payload, "default", ""), ""); err != nil {
			t.Fatal(err)
		}
	}
	enqueue("first")
	pending, err := db.ListPendingNotifications(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	firstID := pending[0].ID
	if err := db.ScheduleNotificationRetry(ctx, firstID, 3, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	enqueue("first")
	enqueue("second")
	enqueue("second")
	if err := db.EnqueueNotification(ctx, sub.ID, anns[0].ID, site.ID, "bark", "test", "second with a renamed site", contentHash("", "second", "default", ""), ""); err != nil {
		t.Fatal(err)
	}
	pending, err = db.ListPendingNotifications(ctx, 10)
	if err != nil || len(pending) != 1 || pending[0].Payload != "second" {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if err := db.MarkNotificationSent(ctx, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	enqueue("second")
	var count, retries int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT retry_count FROM notification_outbox WHERE id=?`, firstID).Scan(&retries); err != nil {
		t.Fatal(err)
	}
	if count != 2 || retries != 3 {
		t.Fatalf("rows=%d retries=%d", count, retries)
	}
}
