package store

import (
	"context"
	"fmt"
	"time"
)

// 续费提醒档位：到期前 3 天内一次（expiring），到期当天再发一次（expiry_day）。
const (
	RenewalReminderKindExpiring  = "expiring"
	RenewalReminderKindExpiryDay = "expiry_day"
)

// RenewalReminderCandidate 是一位需要续费提醒的会员：会员即将到期且拥有至少一个
// 活跃的通知订阅渠道（没有渠道的用户推不到，无需提醒）。
type RenewalReminderCandidate struct {
	UserID    int64
	ExpiresAt time.Time
}

// ListRenewalReminderCandidates 返回到期时间落在 [from, to] 内、且拥有活跃通知订阅的用户；
// 档位判定（提前 3 天 / 到期当天）由调用方完成。
func (store *Store) ListRenewalReminderCandidates(ctx context.Context, from, to time.Time) ([]RenewalReminderCandidate, error) {
	rows, err := store.db.QueryContext(ctx,
		`SELECT u.id, u.membership_expires_at FROM users u
		 WHERE u.membership_expires_at IS NOT NULL AND u.membership_expires_at > ? AND u.membership_expires_at <= ?
		   AND EXISTS (SELECT 1 FROM notification_subscriptions ns WHERE ns.user_id = u.id AND ns.enabled = 1)
		 ORDER BY u.membership_expires_at ASC`, unixMilli(from), unixMilli(to))
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

// ClaimRenewalReminder 抢占某个 (user, 到期时间, 档位) 的提醒发送权：首次抢占返回 true，
// 此前已占位返回 false。必须先占位再发送，杜绝先发后记导致的重复推送。
func (store *Store) ClaimRenewalReminder(ctx context.Context, userID int64, expiresAt time.Time, kind string, now time.Time) (bool, error) {
	res, err := store.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO membership_renewal_reminders (user_id, expires_at, sent_at, kind) VALUES (?, ?, ?, ?)`,
		userID, unixMilli(expiresAt), unixMilli(now), kind)
	if err != nil {
		return false, fmt.Errorf("claim renewal reminder: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReleaseRenewalReminder 撤销占位：全部渠道发送失败时调用，下一轮 tick 可重试。
func (store *Store) ReleaseRenewalReminder(ctx context.Context, userID int64, expiresAt time.Time, kind string) error {
	_, err := store.db.ExecContext(ctx,
		`DELETE FROM membership_renewal_reminders WHERE user_id = ? AND expires_at = ? AND kind = ?`,
		userID, unixMilli(expiresAt), kind)
	if err != nil {
		return fmt.Errorf("release renewal reminder: %w", err)
	}
	return nil
}
