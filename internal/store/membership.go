package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
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
	RegisteredAt        *time.Time `json:"registeredAt,omitempty"`
	Registered          bool       `json:"registered"`
	PreRegistered       bool       `json:"preRegistered"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// ListMembers 列出所有曾开通会员的用户与未登录的预登记记录（含已过期），
// 按到期时间倒序。
func (s *Store) ListMembers(ctx context.Context) ([]MemberUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, name, trust_level, membership_expires_at, registered_at, created_at FROM users WHERE membership_expires_at IS NOT NULL AND membership_expires_at > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	members := make([]MemberUser, 0)
	for rows.Next() {
		var m MemberUser
		var expires *int64
		var created int64
		var registered sql.NullInt64
		if err := rows.Scan(&m.ID, &m.Username, &m.Name, &m.TrustLevel, &expires, &registered, &created); err != nil {
			return nil, err
		}
		m.RegisteredAt = timePtrFromNullMillis(registered)
		m.Registered = m.RegisteredAt != nil
		m.PreRegistered = !m.Registered
		m.CreatedAt = time.UnixMilli(created).UTC()
		if expires != nil && *expires > 0 {
			expiry := time.UnixMilli(*expires).UTC()
			m.MembershipExpiresAt = &expiry
			m.Active = expiry.After(now)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	preRows, err := s.db.QueryContext(ctx, `SELECT username, membership_expires_at, created_at FROM membership_preregistrations WHERE provider = ?`, ProviderLinuxDO)
	if err != nil {
		return nil, err
	}
	defer preRows.Close()
	for preRows.Next() {
		var m MemberUser
		var expires, created int64
		if err := preRows.Scan(&m.Username, &expires, &created); err != nil {
			return nil, err
		}
		expiry := time.UnixMilli(expires).UTC()
		m.MembershipExpiresAt = &expiry
		m.Active = expiry.After(now)
		m.PreRegistered = true
		m.CreatedAt = time.UnixMilli(created).UTC()
		members = append(members, m)
	}
	if err := preRows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(members, func(left, right int) bool {
		leftExpiry, rightExpiry := members[left].MembershipExpiresAt, members[right].MembershipExpiresAt
		if leftExpiry == nil {
			return false
		}
		if rightExpiry == nil {
			return true
		}
		return leftExpiry.After(*rightExpiry)
	})
	return members, nil
}

// SetMembershipByUsername 管理员按 LinuxDO 用户名维护会员有效期。
// 用户已注册时直接更新其会员期；未注册时写入预登记，登录后自动消耗。
func (s *Store) SetMembershipByUsername(ctx context.Context, provider, rawUsername string, expiresAt *time.Time) (MemberUser, error) {
	provider = strings.TrimSpace(provider)
	username, err := normalizeMembershipUsername(rawUsername)
	if err != nil {
		return MemberUser{}, err
	}
	if provider == "" || len(provider) > 100 {
		return MemberUser{}, errors.New("invalid provider")
	}
	var expiry any
	if expiresAt != nil {
		if expiresAt.IsZero() {
			return MemberUser{}, errors.New("invalid membership expiry")
		}
		expiry = unixMilli(*expiresAt)
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemberUser{}, err
	}
	defer tx.Rollback()

	var userID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE provider = ? AND LOWER(username) = ? ORDER BY CASE WHEN registered_at IS NULL THEN 1 ELSE 0 END, id LIMIT 1`, provider, usernameKey(username)).Scan(&userID)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `UPDATE users SET membership_expires_at = ?, updated_at = ? WHERE id = ?`, expiry, unixMilli(now), userID); err != nil {
			return MemberUser{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM membership_preregistrations WHERE provider = ? AND username_key = ?`, provider, usernameKey(username)); err != nil {
			return MemberUser{}, err
		}
		if err := tx.Commit(); err != nil {
			return MemberUser{}, err
		}
		return s.GetMemberByUserID(ctx, userID)
	case errors.Is(err, sql.ErrNoRows):
		if expiresAt == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM membership_preregistrations WHERE provider = ? AND username_key = ?`, provider, usernameKey(username)); err != nil {
				return MemberUser{}, err
			}
			if err := tx.Commit(); err != nil {
				return MemberUser{}, err
			}
			return MemberUser{Username: username, CreatedAt: now}, nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO membership_preregistrations(provider, username, username_key, membership_expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(provider, username_key) DO UPDATE SET username=excluded.username, membership_expires_at=excluded.membership_expires_at, updated_at=excluded.updated_at`, provider, username, usernameKey(username), expiry, unixMilli(now), unixMilli(now)); err != nil {
			return MemberUser{}, err
		}
		if err := tx.Commit(); err != nil {
			return MemberUser{}, err
		}
		return s.GetMembershipPreregistration(ctx, provider, username)
	default:
		return MemberUser{}, err
	}
}

func (s *Store) GetMemberByUsername(ctx context.Context, provider, rawUsername string) (MemberUser, error) {
	provider = strings.TrimSpace(provider)
	username, err := normalizeMembershipUsername(rawUsername)
	if err != nil {
		return MemberUser{}, err
	}
	var userID int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE provider = ? AND LOWER(username) = ? ORDER BY CASE WHEN registered_at IS NULL THEN 1 ELSE 0 END, id LIMIT 1`, provider, usernameKey(username)).Scan(&userID)
	if err == nil {
		return s.GetMemberByUserID(ctx, userID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MemberUser{}, err
	}
	return s.GetMembershipPreregistration(ctx, provider, username)
}

func (s *Store) GetMemberByUserID(ctx context.Context, userID int64) (MemberUser, error) {
	var m MemberUser
	var expires *int64
	var registered sql.NullInt64
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, username, name, trust_level, membership_expires_at, registered_at, created_at FROM users WHERE id = ?`, userID).
		Scan(&m.ID, &m.Username, &m.Name, &m.TrustLevel, &expires, &registered, &created)
	if err != nil {
		return MemberUser{}, err
	}
	m.RegisteredAt = timePtrFromNullMillis(registered)
	m.Registered = m.RegisteredAt != nil
	m.PreRegistered = !m.Registered
	m.CreatedAt = time.UnixMilli(created).UTC()
	applyMemberExpiry(&m, expires, time.Now().UTC())
	return m, nil
}

func (s *Store) GetMembershipPreregistration(ctx context.Context, provider, rawUsername string) (MemberUser, error) {
	provider = strings.TrimSpace(provider)
	username, err := normalizeMembershipUsername(rawUsername)
	if err != nil {
		return MemberUser{}, err
	}
	var m MemberUser
	var expires, created int64
	err = s.db.QueryRowContext(ctx, `SELECT username, membership_expires_at, created_at FROM membership_preregistrations WHERE provider = ? AND username_key = ?`, provider, usernameKey(username)).
		Scan(&m.Username, &expires, &created)
	if err != nil {
		return MemberUser{}, err
	}
	m.PreRegistered = true
	m.CreatedAt = time.UnixMilli(created).UTC()
	applyMemberExpiry(&m, &expires, time.Now().UTC())
	return m, nil
}

func consumeMembershipPreregistration(ctx context.Context, tx *sql.Tx, provider, username string, userID int64, now time.Time) error {
	var expires int64
	err := tx.QueryRowContext(ctx, `SELECT membership_expires_at FROM membership_preregistrations WHERE provider = ? AND username_key = ?`, provider, usernameKey(username)).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET membership_expires_at = MAX(COALESCE(membership_expires_at, 0), ?), updated_at = ? WHERE id = ?`, expires, unixMilli(now), userID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM membership_preregistrations WHERE provider = ? AND username_key = ?`, provider, usernameKey(username))
	return err
}

func normalizeMembershipUsername(value string) (string, error) {
	username := strings.TrimSpace(value)
	username = strings.TrimPrefix(username, "@")
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 200 {
		return "", errors.New("invalid username")
	}
	return username, nil
}

func usernameKey(username string) string {
	return strings.ToLower(username)
}

func applyMemberExpiry(member *MemberUser, expires *int64, now time.Time) {
	if expires == nil || *expires <= 0 {
		return
	}
	expiry := time.UnixMilli(*expires).UTC()
	member.MembershipExpiresAt = &expiry
	member.Active = expiry.After(now)
}
