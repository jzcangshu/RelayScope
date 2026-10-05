-- Keep every existing outbox ID, payload and retry deadline. Only undeliverable
-- outstanding messages are cancelled below; sent/failed history is preserved.
CREATE TABLE notification_outbox_versioned (
    id INTEGER PRIMARY KEY,
    subscription_id INTEGER NOT NULL,
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
-- Historical databases can contain subscriptions already deleted by older
-- builds. Preserve their history, but never send their outstanding messages.
UPDATE notification_outbox_versioned SET status = 'cancelled'
WHERE status IN ('pending', 'retry') AND NOT EXISTS (
    SELECT 1 FROM notification_subscriptions s
    WHERE s.id = notification_outbox_versioned.subscription_id AND s.enabled = 1
);
DROP TABLE notification_outbox;
ALTER TABLE notification_outbox_versioned RENAME TO notification_outbox;
CREATE INDEX idx_outbox_status ON notification_outbox(status, next_retry_at);

-- This ledger includes silent historical backfill versions, not only pushes.
CREATE TABLE announcement_versions (
    announcement_id INTEGER NOT NULL REFERENCES site_announcements(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    PRIMARY KEY(announcement_id, content_hash)
);
