package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// EnqueueNotification queues each full announcement version once per
// subscription. Repeated collection never resets a sent/failed/retrying entry.
// Only legacy rows lack a full-content version; compare their visible content
// conservatively, ignoring the site's display name, before inserting a version.
func (store *Store) EnqueueNotification(ctx context.Context, subscriptionID, announcementID, siteID int64, platform, target, payload, contentVersion, announcementTitle string) error {
	if contentVersion == "" {
		return fmt.Errorf("enqueue notification: content version is required")
	}
	var legacyPayload string
	err := store.db.QueryRowContext(ctx, `SELECT payload FROM notification_outbox WHERE subscription_id = ? AND announcement_id = ? AND content_version = ''`, subscriptionID, announcementID).Scan(&legacyPayload)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("check legacy notification: %w", err)
	}
	if err == nil && sameLegacyAnnouncement(legacyPayload, payload, announcementTitle) {
		return nil
	}
	_, err = store.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO notification_outbox (subscription_id, announcement_id, site_id, platform, target, payload, status, created_at, content_version)
		 SELECT ?, ?, ?, ?, ?, ?, 'pending', ?, ?
		 WHERE EXISTS (SELECT 1 FROM notification_subscriptions WHERE id = ? AND site_id = ? AND enabled = 1)`,
		subscriptionID, announcementID, siteID, platform, target, payload, unixMilli(time.Now().UTC()), contentVersion, subscriptionID, siteID)
	if err != nil {
		return fmt.Errorf("enqueue notification: %w", err)
	}
	return nil
}

func sameLegacyAnnouncement(legacyPayload, payload, announcementTitle string) bool {
	// Legacy messages have the form "📢 site | announcement title" (or
	// "📢 site" when there is no title). They contain at most 2000 body runes;
	// missing full text cannot safely distinguish historical long-text edits.
	type message struct{ Title, Body string }
	var old, next message
	if json.Unmarshal([]byte(legacyPayload), &old) != nil || json.Unmarshal([]byte(payload), &next) != nil {
		return legacyPayload == payload
	}
	if old.Body != next.Body {
		return false
	}
	// Site names may themselves contain the separator. Match the known raw
	// announcement title as a suffix instead of guessing where the site ends.
	// An untitled legacy message cannot be distinguished from a site's name;
	// prefer suppressing ambiguous historical content over replaying it.
	return announcementTitle == "" || strings.HasSuffix(old.Title, " | "+announcementTitle)
}

// ListPendingNotifications returns outbox entries that are ready to send.
func (store *Store) ListPendingNotifications(ctx context.Context, limit int) ([]NotificationOutboxEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	now := unixMilli(time.Now().UTC())
	rows, err := store.db.QueryContext(ctx,
		`SELECT id, subscription_id, announcement_id, site_id, platform, target, payload, status, retry_count, next_retry_at, created_at, sent_at
		 FROM notification_outbox
		 WHERE (status = 'pending' OR (status = 'retry' AND next_retry_at <= ?))
		 AND EXISTS (SELECT 1 FROM notification_subscriptions s WHERE s.id = notification_outbox.subscription_id AND s.enabled = 1)
		 ORDER BY created_at ASC
		 LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending notifications: %w", err)
	}
	defer rows.Close()
	var entries []NotificationOutboxEntry
	for rows.Next() {
		var e NotificationOutboxEntry
		var nextRetryAt, sentAt *int64
		var createdAt int64
		if err := rows.Scan(&e.ID, &e.SubscriptionID, &e.AnnouncementID, &e.SiteID, &e.Platform, &e.Target, &e.Payload, &e.Status, &e.RetryCount, &nextRetryAt, &createdAt, &sentAt); err != nil {
			return nil, fmt.Errorf("scan outbox entry: %w", err)
		}
		if nextRetryAt != nil {
			t := time.UnixMilli(*nextRetryAt).UTC()
			e.NextRetryAt = &t
		}
		if sentAt != nil {
			t := time.UnixMilli(*sentAt).UTC()
			e.SentAt = &t
		}
		e.CreatedAt = time.UnixMilli(createdAt).UTC()
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// NotificationCanSend rechecks cancellation after a dispatcher has read a
// batch. It cannot recall a request that is already in flight to a platform.
func (store *Store) NotificationCanSend(ctx context.Context, id int64) (bool, error) {
	var allowed bool
	err := store.db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM notification_outbox o JOIN notification_subscriptions s ON s.id = o.subscription_id
		WHERE o.id = ? AND o.status IN ('pending', 'retry') AND s.enabled = 1)`, id).Scan(&allowed)
	return allowed, err
}

// MarkNotificationSent marks an outbox entry as successfully sent.
func (store *Store) MarkNotificationSent(ctx context.Context, id int64) error {
	_, err := store.db.ExecContext(ctx,
		`UPDATE notification_outbox SET status = 'sent', sent_at = ? WHERE id = ?`,
		unixMilli(time.Now().UTC()), id)
	return err
}

// MarkNotificationFailed marks an outbox entry as permanently failed.
func (store *Store) MarkNotificationFailed(ctx context.Context, id int64) error {
	_, err := store.db.ExecContext(ctx,
		`UPDATE notification_outbox SET status = 'failed' WHERE id = ?`, id)
	return err
}

// ScheduleNotificationRetry updates retry count and next retry time.
func (store *Store) ScheduleNotificationRetry(ctx context.Context, id int64, retryCount int, nextRetry time.Time) error {
	_, err := store.db.ExecContext(ctx,
		`UPDATE notification_outbox SET status = 'retry', retry_count = ?, next_retry_at = ? WHERE id = ?`,
		retryCount, unixMilli(nextRetry), id)
	return err
}

// RecordDelivery inserts a delivery record.
func (store *Store) RecordDelivery(ctx context.Context, subscriptionID, announcementID, siteID int64, platform, status, errorMessage string) error {
	_, err := store.db.ExecContext(ctx,
		`INSERT INTO notification_deliveries (subscription_id, announcement_id, site_id, platform, status, error_message, sent_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		subscriptionID, announcementID, siteID, platform, status, errorMessage, unixMilli(time.Now().UTC()))
	return err
}

// ListActiveSubscriptionsForSite returns all enabled subscriptions for a site.
func (store *Store) ListActiveSubscriptionsForSite(ctx context.Context, siteID int64) ([]NotificationSubscription, error) {
	rows, err := store.db.QueryContext(ctx,
		`SELECT ns.id, ns.user_id, ns.site_id, ns.platform, ns.target, ns.config, ns.enabled, ns.created_at, ns.updated_at
		 FROM notification_subscriptions ns
		 WHERE ns.site_id = ? AND ns.enabled = 1`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions for site: %w", err)
	}
	defer rows.Close()
	var subs []NotificationSubscription
	for rows.Next() {
		var s NotificationSubscription
		var createdAt, updatedAt int64
		if err := rows.Scan(&s.ID, &s.UserID, &s.SiteID, &s.Platform, &s.Target, &s.Config, &s.Enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		s.CreatedAt = time.UnixMilli(createdAt).UTC()
		s.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// --- User subscription CRUD ---

// DistinctSubscriptionSites 返回用户全部订阅的去重站点 ID，按最早一条订阅的 id 升序。
// 免费额度（3 个站点）与推送暂停规则都以这个顺序为准，前端「已暂停」徽标用同一规则计算。
func (store *Store) DistinctSubscriptionSites(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := store.db.QueryContext(ctx,
		`SELECT site_id FROM notification_subscriptions WHERE user_id = ? GROUP BY site_id ORDER BY MIN(id)`, userID)
	if err != nil {
		return nil, fmt.Errorf("list distinct subscription sites: %w", err)
	}
	defer rows.Close()
	var sites []int64
	for rows.Next() {
		var siteID int64
		if err := rows.Scan(&siteID); err != nil {
			return nil, fmt.Errorf("scan distinct subscription site: %w", err)
		}
		sites = append(sites, siteID)
	}
	return sites, rows.Err()
}

// CreateSubscription creates a new notification subscription.
func (store *Store) CreateSubscription(ctx context.Context, userID, siteID int64, platform, target, config string) (NotificationSubscription, error) {
	now := unixMilli(time.Now().UTC())
	res, err := store.db.ExecContext(ctx,
		`INSERT INTO notification_subscriptions (id, user_id, site_id, platform, target, config, created_at, updated_at)
		 VALUES ((SELECT COALESCE(MAX(id), 0) + 1 FROM (
		 SELECT id FROM notification_subscriptions UNION ALL
		 SELECT subscription_id FROM notification_outbox UNION ALL
		 SELECT subscription_id FROM notification_deliveries)), ?, ?, ?, ?, ?, ?, ?)`,
		userID, siteID, platform, target, config, now, now)
	if err != nil {
		return NotificationSubscription{}, fmt.Errorf("create subscription: %w", err)
	}
	id, _ := res.LastInsertId()
	return NotificationSubscription{
		ID: id, UserID: userID, SiteID: siteID,
		Platform: platform, Target: target, Config: config,
		Enabled:   true,
		CreatedAt: time.UnixMilli(now).UTC(),
		UpdatedAt: time.UnixMilli(now).UTC(),
	}, nil
}

// ListUserSubscriptions returns all subscriptions for a user.
func (store *Store) ListUserSubscriptions(ctx context.Context, userID int64) ([]NotificationSubscription, error) {
	rows, err := store.db.QueryContext(ctx,
		`SELECT ns.id, ns.user_id, ns.site_id, s.name, ns.platform, ns.target, ns.config, ns.enabled, ns.created_at, ns.updated_at
		 FROM notification_subscriptions ns
		 JOIN sites s ON s.id = ns.site_id
		 WHERE ns.user_id = ?
		 ORDER BY ns.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user subscriptions: %w", err)
	}
	defer rows.Close()
	var subs []NotificationSubscription
	for rows.Next() {
		var s NotificationSubscription
		var createdAt, updatedAt int64
		if err := rows.Scan(&s.ID, &s.UserID, &s.SiteID, &s.SiteName, &s.Platform, &s.Target, &s.Config, &s.Enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		s.CreatedAt = time.UnixMilli(createdAt).UTC()
		s.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// UpdateSubscriptionChannel changes the platform and target on every
// subscription a user owns, so rotating a key or switching channels applies to
// already-subscribed sites in one shot instead of forcing the user to re-toggle
// each site. Pending outbox entries carry a denormalized copy of the target, so
// they are repointed too — otherwise a notification queued seconds earlier
// would still fly to the old channel.
func (store *Store) UpdateSubscriptionChannel(ctx context.Context, userID int64, platform, target string) (int64, error) {
	now := unixMilli(time.Now().UTC())
	res, err := store.db.ExecContext(ctx,
		`UPDATE notification_subscriptions SET platform = ?, target = ?, updated_at = ? WHERE user_id = ?`,
		platform, target, now, userID)
	if err != nil {
		return 0, fmt.Errorf("update subscription channel: %w", err)
	}
	count, _ := res.RowsAffected()
	if count > 0 {
		if _, err := store.db.ExecContext(ctx,
			`UPDATE notification_outbox SET platform = ?, target = ?
			 WHERE subscription_id IN (SELECT id FROM notification_subscriptions WHERE user_id = ?)
			 AND status IN ('pending', 'retry')`,
			platform, target, userID); err != nil {
			return count, fmt.Errorf("repoint pending notifications: %w", err)
		}
	}
	return count, nil
}

// DeleteSubscription removes a subscription (only if owned by the user).
func (store *Store) DeleteSubscription(ctx context.Context, userID, subscriptionID int64) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete subscription: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET status = 'cancelled'
		WHERE subscription_id = ? AND status IN ('pending', 'retry')
		AND EXISTS (SELECT 1 FROM notification_subscriptions WHERE id = ? AND user_id = ?)`, subscriptionID, subscriptionID, userID); err != nil {
		return fmt.Errorf("cancel subscription notifications: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`DELETE FROM notification_subscriptions WHERE id = ? AND user_id = ?`,
		subscriptionID, userID)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("subscription not found")
	}
	return tx.Commit()
}
