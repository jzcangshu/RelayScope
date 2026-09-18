package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// The listing queries scan INTEGER millisecond columns; an empty table hides a
// mismatched scan target because rows.Scan is never called. These tests always
// seed rows first so a bad scan target fails loudly.
func TestListSiteAnnouncementsReturnsStoredRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	site := createTestSite(t, store)

	now := time.Now().UTC().Truncate(time.Millisecond)
	older := now.Add(-20 * 24 * time.Hour)
	newer := now.Add(-2 * time.Hour)

	news, err := store.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{
		{ExternalID: "older", Content: "旧公告", AnnType: "warning", PublishedAt: older},
		{ExternalID: "newer", Content: "新公告", AnnType: "success", PublishedAt: newer},
	}, now)
	if err != nil {
		t.Fatalf("apply announcements: %v", err)
	}
	if len(news) != 2 {
		t.Fatalf("new announcements = %d, want 2", len(news))
	}

	items, err := store.ListSiteAnnouncements(ctx, site.ID, 10)
	if err != nil {
		t.Fatalf("list site announcements: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("listed announcements = %d, want 2", len(items))
	}
	// Newest first.
	if items[0].ExternalID != "newer" || items[1].ExternalID != "older" {
		t.Fatalf("order = %q, %q; want newer, older", items[0].ExternalID, items[1].ExternalID)
	}
	if !items[0].PublishedAt.Equal(newer) {
		t.Fatalf("publishedAt = %s, want %s", items[0].PublishedAt, newer)
	}
	if !items[0].FirstSeenAt.Equal(now) || !items[0].LastSeenAt.Equal(now) {
		t.Fatalf("first/last seen = %s/%s, want %s", items[0].FirstSeenAt, items[0].LastSeenAt, now)
	}
	if items[0].RemovedAt != nil {
		t.Fatalf("removedAt = %s, want nil", items[0].RemovedAt)
	}
	if items[0].AnnType != "success" || items[0].Content != "新公告" {
		t.Fatalf("announcement = %+v, want success/新公告", items[0])
	}
}

func TestListAllRecentAnnouncementsIncludesSiteName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	site := createTestSite(t, store)

	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := store.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{
		{ExternalID: "only", Content: "公告内容", PublishedAt: now.Add(-time.Hour)},
	}, now); err != nil {
		t.Fatalf("apply announcements: %v", err)
	}

	items, err := store.ListAllRecentAnnouncements(ctx, 10)
	if err != nil {
		t.Fatalf("list all recent announcements: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("listed announcements = %d, want 1", len(items))
	}
	if items[0].SiteName != site.Name || items[0].SiteID != site.ID {
		t.Fatalf("site = %q/%d, want %q/%d", items[0].SiteName, items[0].SiteID, site.Name, site.ID)
	}
}

func TestApplyAnnouncementsReportsOnlyNewOrChanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	site := createTestSite(t, store)

	now := time.Now().UTC()
	input := AnnouncementInput{ExternalID: "x", Content: "第一版", PublishedAt: now.Add(-time.Hour)}
	if news, err := store.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{input}, now); err != nil || len(news) != 1 {
		t.Fatalf("first apply = %d news, err %v; want 1", len(news), err)
	}
	// Unchanged re-collection must stay silent.
	if news, err := store.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{input}, now); err != nil || len(news) != 0 {
		t.Fatalf("repeat apply = %d news, err %v; want 0", len(news), err)
	}
	// Edited content is a new event.
	input.Content = "第二版"
	if news, err := store.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{input}, now); err != nil || len(news) != 1 {
		t.Fatalf("edited apply = %d news, err %v; want 1", len(news), err)
	}
	items, err := store.ListSiteAnnouncements(ctx, site.ID, 10)
	if err != nil {
		t.Fatalf("list site announcements: %v", err)
	}
	if len(items) != 1 || items[0].Content != "第二版" {
		t.Fatalf("stored announcements = %+v, want a single 第二版 row", items)
	}
}

func TestSiteIDsWithAnnouncementsReportsOnlySitesWithRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	withAnnouncements := createTestSite(t, store)
	// base_url is unique, so the second site needs its own URL.
	without, err := store.CreateSite(ctx, Site{
		Name: "Quiet", BaseURL: "https://quiet.example.test", SourceURL: "https://quiet.example.test/pricing",
		AdapterKey: "test", Enabled: true, Interval: 20 * time.Minute, Jitter: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create quiet site: %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.ApplyAnnouncements(ctx, withAnnouncements.ID, []AnnouncementInput{
		{ExternalID: "a", Content: "有公告", PublishedAt: now},
	}, now); err != nil {
		t.Fatalf("apply announcements: %v", err)
	}

	ids, err := store.SiteIDsWithAnnouncements(ctx)
	if err != nil {
		t.Fatalf("site ids with announcements: %v", err)
	}
	if len(ids) != 1 || ids[0] != withAnnouncements.ID {
		t.Fatalf("site ids = %v, want [%d] (site %d has no announcements)", ids, withAnnouncements.ID, without.ID)
	}
}

func TestApplyAnnouncementsSkipsEntriesWithoutExternalID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	site := createTestSite(t, store)

	now := time.Now().UTC()
	news, err := store.ApplyAnnouncements(ctx, site.ID, []AnnouncementInput{
		{ExternalID: "  ", Content: "无 ID"},
		{ExternalID: "kept", Content: "有 ID"},
	}, now)
	if err != nil {
		t.Fatalf("apply announcements: %v", err)
	}
	if len(news) != 1 || news[0].ExternalID != "kept" {
		t.Fatalf("news = %+v, want only the entry with an external ID", news)
	}
}

func TestListSiteAnnouncementsDefaultsLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	site := createTestSite(t, store)

	now := time.Now().UTC()
	inputs := make([]AnnouncementInput, 0, 60)
	for i := 0; i < 60; i++ {
		inputs = append(inputs, AnnouncementInput{
			ExternalID:  "ann-" + strconv.Itoa(i),
			Content:     "公告",
			PublishedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	if _, err := store.ApplyAnnouncements(ctx, site.ID, inputs, now); err != nil {
		t.Fatalf("apply announcements: %v", err)
	}

	items, err := store.ListSiteAnnouncements(ctx, site.ID, 0)
	if err != nil {
		t.Fatalf("list site announcements: %v", err)
	}
	if len(items) != 50 {
		t.Fatalf("listed announcements = %d, want the 50-row default limit", len(items))
	}
}