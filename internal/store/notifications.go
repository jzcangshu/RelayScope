package store

import (
	"context"
	"fmt"
	"time"
)

// EnqueueNotification inserts a notification into the outbox.
func (store *Store) EnqueueNotification(ctx context.Context, subscriptionID, announcementID, siteID int64, platform, target, payload string) error {
	_, err := store.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO notification_outbox (subscription_id, announcement_id, site_id, platform, target, payload, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'pending', ?)`,
		subscriptionID, announcementID, siteID, platform, target, payload, unixMilli(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("enqueue notification: %w", err)
	}
	return nil
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
		if err := rows.Scan(&e.ID, &e.SubscriptionID, &e.AnnouncementID, &e.SiteID, &e.Platform, &e.Target, &e.Payload, &e.Status, &e.RetryCount, &nextRetryAt, &e.CreatedAt, &sentAt); err != nil {
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
		e.CreatedAt = time.UnixMilli(e.CreatedAt.UnixMilli()).UTC()
		entries = append(entries, e)
	}
	return entries, rows.Err()
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
		if err := rows.Scan(&s.ID, &s.UserID, &s.SiteID, &s.Platform, &s.Target, &s.Config, &s.Enabled, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		s.CreatedAt = time.UnixMilli(s.CreatedAt.UnixMilli()).UTC()
		s.UpdatedAt = time.UnixMilli(s.UpdatedAt.UnixMilli()).UTC()
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// --- User subscription CRUD ---

// CreateSubscription creates a new notification subscription.
func (store *Store) CreateSubscription(ctx context.Context, userID, siteID int64, platform, target, config string) (NotificationSubscription, error) {
	now := unixMilli(time.Now().UTC())
	res, err := store.db.ExecContext(ctx,
		`INSERT INTO notification_subscriptions (user_id, site_id, platform, target, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, siteID, platform, target, config, now, now)
	if err != nil {
		return NotificationSubscription{}, fmt.Errorf("create subscription: %w", err)
	}
	id, _ := res.LastInsertId()
	return NotificationSubscription{
		ID: id, UserID: userID, SiteID: siteID,
		Platform: platform, Target: target, Config: config,
		Enabled: true,
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
		if err := rows.Scan(&s.ID, &s.UserID, &s.SiteID, &s.SiteName, &s.Platform, &s.Target, &s.Config, &s.Enabled, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		s.CreatedAt = time.UnixMilli(s.CreatedAt.UnixMilli()).UTC()
		s.UpdatedAt = time.UnixMilli(s.UpdatedAt.UnixMilli()).UTC()
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

// DeleteSubscription removes a subscription (only if owned by the user).
func (store *Store) DeleteSubscription(ctx context.Context, userID, subscriptionID int64) error {
	result, err := store.db.ExecContext(ctx,
		`DELETE FROM notification_subscriptions WHERE id = ? AND user_id = ?`,
		subscriptionID, userID)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("subscription not found")
	}
	return nil
}
