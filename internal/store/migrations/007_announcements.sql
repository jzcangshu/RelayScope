-- 站点公告采集与推送通知

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
