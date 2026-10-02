package store

import (
	"context"
	"fmt"
	"time"
)

// RenewalReminderCandidate 是一位需要续费提醒的会员：会员即将到期且拥有至少一个
// 活跃的通知订阅渠道（没有渠道的用户推不到，无需提醒）。
type RenewalReminderCandidate struct {
	UserID    int64
	ExpiresAt time.Time
}

// ListRenewalReminderCandidates 返回会员在 deadline 前到期、且拥有活跃通知订阅的用户。
func (store *Store) ListRenewalReminderCandidates(ctx context.Context, deadline time.Time) ([]RenewalReminderCandidate, error) {
	now := unixMilli(time.Now().UTC())
	rows, err := store.db.QueryContext(ctx,
		`SELECT u.id, u.membership_expires_at FROM users u
		 WHERE u.membership_expires_at IS NOT NULL AND u.membership_expires_at > ? AND u.membership_expires_at <= ?
		   AND EXISTS (SELECT 1 FROM notification_subscriptions ns WHERE ns.user_id = u.id AND ns.enabled = 1)
		 ORDER BY u.membership_expires_at ASC`, now, unixMilli(deadline))
	if err != nil {
		return nil, fmt.Errorf("list renewal reminder candidates: %w", err)
	}
	defer rows.Close()
	var candidates []RenewalReminderCandidate
	for rows.Next() {
		var c RenewalReminderCandidate
		var expires int64
		if err := rows.Scan(&c.UserID, &expires); err != nil {
			return nil, fmt.Errorf("scan renewal reminder candidate: %w", err)
		}
		c.ExpiresAt = time.UnixMilli(expires).UTC()
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

// MarkRenewalReminderSent 记录某个 (user, 到期时间) 的提醒已发送；返回 false 表示此前已提醒过，
// 调用方据此去重——同一有效期最多提醒一次。
func (store *Store) MarkRenewalReminderSent(ctx context.Context, userID int64, expiresAt, now time.Time) (bool, error) {
	res, err := store.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO membership_renewal_reminders (user_id, expires_at, sent_at) VALUES (?, ?, ?)`,
		userID, unixMilli(expiresAt), unixMilli(now))
	if err != nil {
		return false, fmt.Errorf("mark renewal reminder sent: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
