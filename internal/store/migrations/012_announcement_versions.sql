-- Keep every existing outbox ID, payload, delivery state and retry deadline.
CREATE TABLE notification_outbox_versioned (
    id INTEGER PRIMARY KEY,
    subscription_id INTEGER NOT NULL REFERENCES notification_subscriptions(id),
    announcement_id INTEGER NOT NULL REFERENCES site_announcements(id),
    site_id INTEGER NOT NULL,
    platform TEXT NOT NULL,
    target TEXT NOT NULL,
    payload TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    retry_count INTEGER NOT NULL DEFAULT 0,
    next_retry_at INTEGER,
    created_at INTEGER NOT NULL,
    sent_at INTEGER,
    content_version TEXT NOT NULL DEFAULT '',
    UNIQUE(subscription_id, announcement_id, content_version)
);
INSERT INTO notification_outbox_versioned
SELECT *, '' FROM notification_outbox;
DROP TABLE notification_outbox;
ALTER TABLE notification_outbox_versioned RENAME TO notification_outbox;
CREATE INDEX idx_outbox_status ON notification_outbox(status, next_retry_at);

-- This ledger includes silent historical backfill versions, not only pushes.
CREATE TABLE announcement_versions (
    announcement_id INTEGER NOT NULL REFERENCES site_announcements(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    PRIMARY KEY(announcement_id, content_hash)
);
