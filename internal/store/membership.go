package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Membership 描述用户的会员状态。ExpiresAt 为 nil 表示从未开通。
type Membership struct {
	ExpiresAt *time.Time `json:"expiresAt"`
	Active    bool       `json:"active"`
}

const dayMillis = int64(24 * 60 * 60 * 1000)

// ExtendMembership 按"延期"语义延长会员：从当前有效期或现在中较晚者起算再加 days 天。
func (s *Store) ExtendMembership(ctx context.Context, userID int64, days int64) (Membership, error) {
	if userID <= 0 || days <= 0 || days > 3650 {
		return Membership{}, errors.New("invalid membership extension")
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET membership_expires_at = MAX(COALESCE(membership_expires_at, 0), ?) + ?, updated_at = ? WHERE id = ?`, unixMilli(now), days*dayMillis, unixMilli(now), userID); err != nil {
		return Membership{}, err
	}
	return s.GetMembership(ctx, userID)
}

func (s *Store) GetMembership(ctx context.Context, userID int64) (Membership, error) {
	var expires *int64
	if err := s.db.QueryRowContext(ctx, `SELECT membership_expires_at FROM users WHERE id = ?`, userID).Scan(&expires); err != nil {
		return Membership{}, err
	}
	return membershipFromMillis(expires, time.Now().UTC()), nil
}

func membershipFromMillis(expires *int64, now time.Time) Membership {
	if expires == nil || *expires <= 0 {
		return Membership{}
	}
	expiry := time.UnixMilli(*expires).UTC()
	return Membership{ExpiresAt: &expiry, Active: expiry.After(now)}
}

// GetSetting 读取运营设置（存于 app_meta）。未设置时返回 fallback。
func (s *Store) GetSetting(ctx context.Context, key, fallback string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fallback, nil
		}
		return "", err
	}
	return value, nil
}

// SetSetting 写入运营设置。键的白名单校验由调用方（路由层）负责。
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// MemberUser 描述一个有过会员记录的用户（含过期）。
type MemberUser struct {
	ID                  int64      `json:"id"`
	Username            string     `json:"username"`
	Name                string     `json:"name"`
	TrustLevel          int        `json:"trustLevel"`
	MembershipExpiresAt *time.Time `json:"membershipExpiresAt"`
	Active              bool       `json:"active"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// ListMembers 列出所有曾开通会员的用户（含已过期），按到期时间倒序。
func (s *Store) ListMembers(ctx context.Context) ([]MemberUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, name, trust_level, membership_expires_at, created_at FROM users WHERE membership_expires_at IS NOT NULL AND membership_expires_at > 0 ORDER BY membership_expires_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	var members []MemberUser
	for rows.Next() {
		var m MemberUser
		var expires *int64
		var created int64
		if err := rows.Scan(&m.ID, &m.Username, &m.Name, &m.TrustLevel, &expires, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = time.UnixMilli(created).UTC()
		if expires != nil && *expires > 0 {
			expiry := time.UnixMilli(*expires).UTC()
			m.MembershipExpiresAt = &expiry
			m.Active = expiry.After(now)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
