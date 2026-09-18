# Announcement Monitoring & Push Notification Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add per-site announcement monitoring (piggybacking on existing collection) and a subscription-based push notification system (Telegram, Feishu, Bark).

**Architecture:** Announcements are collected via a new optional `AnnouncementProvider` interface on adapters. NewAPI adapters reuse the already-fetched `/api/status` body (zero extra HTTP). Sub2API uses `/api/v1/announcements` with Bearer auth. Notifications flow through a SQLite outbox table consumed by a dispatcher goroutine with per-platform rate limiting.

**Tech Stack:** Go stdlib + net/http, SQLite (modernc.org/sqlite), existing adapter/collector/scheduler pattern.

---

## Task 1: Database Migration

**Files:**
- Create: `internal/store/migrations/007_announcements.sql`

**Step 1: Write the migration**

```sql
-- 007_announcements.sql
-- Announcement monitoring and push notification tables

CREATE TABLE IF NOT EXISTS site_announcements (
    id            INTEGER PRIMARY KEY,
    site_id       INTEGER NOT NULL REFERENCES sites(id),
    external_id   TEXT    NOT NULL,
    title         TEXT    NOT NULL DEFAULT '',
    content       TEXT    NOT NULL,
    ann_type      TEXT    NOT NULL DEFAULT 'default',
    extra         TEXT    NOT NULL DEFAULT '',
    content_hash  TEXT    NOT NULL,
    published_at  INTEGER NOT NULL,
    first_seen_at INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL,
    removed_at    INTEGER,
    UNIQUE(site_id, external_id)
);

CREATE TABLE IF NOT EXISTS notification_subscriptions (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users(id),
    site_id       INTEGER NOT NULL REFERENCES sites(id),
    platform      TEXT    NOT NULL,
    target        TEXT    NOT NULL,
    config        TEXT    NOT NULL DEFAULT '{}',
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE(user_id, site_id, platform, target)
);

CREATE TABLE IF NOT EXISTS notification_outbox (
    id              INTEGER PRIMARY KEY,
    subscription_id INTEGER NOT NULL REFERENCES notification_subscriptions(id),
    announcement_id INTEGER NOT NULL REFERENCES site_announcements(id),
    site_id         INTEGER NOT NULL,
    platform        TEXT    NOT NULL,
    target          TEXT    NOT NULL,
    payload         TEXT    NOT NULL,
    status          TEXT    NOT NULL DEFAULT 'pending',
    retry_count     INTEGER NOT NULL DEFAULT 0,
    next_retry_at   INTEGER,
    created_at      INTEGER NOT NULL,
    sent_at         INTEGER,
    UNIQUE(subscription_id, announcement_id)
);

CREATE TABLE IF NOT EXISTS notification_deliveries (
    id              INTEGER PRIMARY KEY,
    subscription_id INTEGER NOT NULL,
    announcement_id INTEGER NOT NULL,
    site_id         INTEGER NOT NULL,
    platform        TEXT    NOT NULL,
    status          TEXT    NOT NULL,
    error_message   TEXT    NOT NULL DEFAULT '',
    sent_at         INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_announcements_site ON site_announcements(site_id, removed_at);
CREATE INDEX IF NOT EXISTS idx_announcements_hash ON site_announcements(site_id, content_hash);
CREATE INDEX IF NOT EXISTS idx_outbox_status ON notification_outbox(status, next_retry_at);
CREATE INDEX IF NOT EXISTS idx_subscriptions_site ON notification_subscriptions(site_id, enabled);
CREATE INDEX IF NOT EXISTS idx_deliveries_sub ON notification_deliveries(subscription_id, announcement_id);
```

**Step 2: Run tests to verify migration applies**

Run: `go test ./internal/store/ -run TestMigrate`
Expected: PASS

---

## Task 2: Domain Types & Store Layer

**Files:**
- Create: `internal/store/announcements.go`
- Modify: `internal/store/types.go` (add new types)

**Step 1: Add types to types.go**

```go
type SiteAnnouncement struct {
    ID          int64     `json:"id"`
    SiteID      int64     `json:"siteId"`
    SiteName    string    `json:"siteName,omitempty"`
    ExternalID  string    `json:"externalId"`
    Title       string    `json:"title"`
    Content     string    `json:"content"`
    AnnType     string    `json:"annType"`
    Extra       string    `json:"extra"`
    ContentHash string    `json:"-"`
    PublishedAt time.Time `json:"publishedAt"`
    FirstSeenAt time.Time `json:"firstSeenAt"`
    LastSeenAt  time.Time `json:"lastSeenAt"`
    RemovedAt   *time.Time `json:"removedAt,omitempty"`
}

type NotificationSubscription struct {
    ID        int64     `json:"id"`
    UserID    int64     `json:"userId"`
    SiteID    int64     `json:"siteId"`
    SiteName  string    `json:"siteName,omitempty"`
    Platform  string    `json:"platform"`
    Target    string    `json:"target"`
    Config    string    `json:"config"`
    Enabled   bool      `json:"enabled"`
    CreatedAt time.Time `json:"createdAt"`
    UpdatedAt time.Time `json:"updatedAt"`
}

type NotificationOutboxEntry struct {
    ID             int64      `json:"id"`
    SubscriptionID int64      `json:"subscriptionId"`
    AnnouncementID int64      `json:"announcementId"`
    SiteID         int64      `json:"siteId"`
    Platform       string     `json:"platform"`
    Target         string     `json:"target"`
    Payload        string     `json:"payload"`
    Status         string     `json:"status"`
    RetryCount     int        `json:"retryCount"`
    NextRetryAt    *time.Time `json:"nextRetryAt,omitempty"`
    CreatedAt      time.Time  `json:"createdAt"`
    SentAt         *time.Time `json:"sentAt,omitempty"`
}
```

**Step 2: Implement store/announcements.go**

Key methods:
- `ApplyAnnouncements(ctx, siteID, []AnnouncementInput, now) ([]SiteAnnouncement, error)` - upserts, returns newly appeared announcements
- `ListSiteAnnouncements(ctx, siteID, limit) ([]SiteAnnouncement, error)`
- `ListAllRecentAnnouncements(ctx, limit) ([]SiteAnnouncement, error)`
- `CleanupOldAnnouncements(ctx, maxAge) (int64, error)`

**Step 3: Run tests**

Run: `go build ./...`
Expected: PASS

---

## Task 3: Adapter Interface & NewAPI Implementation

**Files:**
- Modify: `internal/adapter/adapter.go` (add AnnouncementProvider interface)
- Modify: `internal/adapter/probe.go` (implement AnnouncementProvider for ProbeAdapter)
- Modify: `internal/adapter/newapi.go` (add announcementMode to config)

**Step 1: Add interface to adapter.go**

```go
type Announcement struct {
    ExternalID  string
    Title       string
    Content     string
    Type        string
    Extra       string
    PublishedAt time.Time
}

type AnnouncementProvider interface {
    CollectAnnouncements(ctx context.Context, site Site, fetcher Fetcher) ([]Announcement, error)
}
```

**Step 2: Add announcementMode to ProbeAdapter config schema**

Add to ConfigSchema: `"announcementMode":{"type":"string","enum":["timeline","notice_diff","disabled"],"default":"timeline"}`

**Step 3: Implement for ProbeAdapter**

- Cache `/api/status` response body during Collect()
- timeline mode: parse `data.announcements[]` from cached body
- notice_diff mode: fetch `/api/notice`, return as single Announcement
- disabled mode: return nil

**Step 4: Build & test**

Run: `go build ./...`
Expected: PASS

---

## Task 4: Sub2API Announcement Implementation

**Files:**
- Modify: `internal/adapter/sub2monitor.go` (implement AnnouncementProvider)

**Step 1: Implement CollectAnnouncements**

- Fetch `GET /api/v1/announcements` with existing fetcher (inherits session cookies/headers)
- Parse response: `{code:0, data:[{id, title, content, notify_mode, created_at, updated_at}]}`
- Convert to []Announcement with ExternalID = strconv.Itoa(id)

**Step 2: Build**

Run: `go build ./...`
Expected: PASS

---

## Task 5: Collector Integration

**Files:**
- Modify: `internal/collector/collector.go` (add announcement collection after model collection)

**Step 1: Add announcement collection in CollectSite()**

After successful `store.ApplyCollection()` and before `FinishCollectionRun()`:
```go
if provider, ok := adapterImpl.(adapter.AnnouncementProvider); ok {
    anns, annErr := provider.CollectAnnouncements(ctx, siteDefinition, fetcher)
    if annErr != nil {
        collector.logger.Warn("announcement collection failed", "site_id", site.ID, "error", annErr)
    } else if len(anns) > 0 {
        newAnns, err := collector.store.ApplyAnnouncements(ctx, site.ID, anns, now)
        if err != nil {
            collector.logger.Warn("apply announcements failed", "site_id", site.ID, "error", err)
        } else if len(newAnns) > 0 {
            // Enqueue notifications (async, non-blocking)
            go collector.enqueueNotifications(newAnns)
        }
    }
}
```

**Step 2: Build**

Run: `go build ./...`
Expected: PASS

---

## Task 6: Admin & Public API Endpoints

**Files:**
- Modify: `internal/httpserver/admin_routes.go`
- Modify: `internal/httpserver/routes.go`

**Step 1: Admin endpoints**

- `GET /api/v1/admin/sites/{id}/announcements` - list site announcements
- Add `announcementMode` to site update payload handling

**Step 2: Public endpoints**

- `GET /api/v1/public/site-announcements?site_id=X` - recent announcements for a site

**Step 3: Build**

Run: `go build ./...`
Expected: PASS

---

## Task 7: Notification Dispatcher & Senders

**Files:**
- Create: `internal/notifier/dispatcher.go`
- Create: `internal/notifier/telegram.go`
- Create: `internal/notifier/feishu.go`
- Create: `internal/notifier/bark.go`
- Create: `internal/notifier/ratelimit.go`

**Step 1: Implement dispatcher**

- Goroutine polling outbox every 10s
- Group by platform, apply rate limits, send sequentially
- Mark sent/failed, exponential backoff

**Step 2: Implement 3 platform senders**

- Telegram: POST /bot{token}/sendMessage
- Feishu: POST /open-apis/bot/v2/hook/{token}
- Bark: POST https://api.day.app/{key}/{title}/{body}

**Step 3: Build**

Run: `go build ./...`
Expected: PASS

---

## Task 8: User Subscription API

**Files:**
- Modify: `internal/httpserver/user_routes.go`
- Modify: `internal/store/` (add subscription CRUD)

**Step 1: Subscription CRUD**

- POST /api/v1/user/notification-subscriptions
- GET /api/v1/user/notification-subscriptions
- DELETE /api/v1/user/notification-subscriptions/{id}

**Step 2: Build**

Run: `go build ./...`
Expected: PASS

---

## Task 9: Frontend - Admin Announcement Config

**Files:**
- Modify: `web/admin/admin.html` (site edit form)
- Modify: `web/admin/admin.js` (announcement mode dropdown + preview)

**Step 1: Add announcement mode dropdown to site edit form**

**Step 2: Add announcement preview section**

---

## Task 10: Frontend - Public Announcement Display

**Files:**
- Modify: `web/public/dashboard.js` (bell icon, announcement tab, subscription UI)

**Step 1: Add bell icon to cards**

**Step 2: Add announcement tab to site detail dialog**

**Step 3: Add notification subscription management to user settings**

---

## Implementation Order

1. Task 1: DB migration
2. Task 2: Domain types + store
3. Task 3: Adapter interface + NewAPI impl
4. Task 4: Sub2API impl
5. Task 5: Collector integration
6. Task 6: API endpoints
7. Task 7: Notifier dispatcher + senders
8. Task 8: User subscription API
9. Task 9: Frontend admin
10. Task 10: Frontend public
