package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

const userSessionTTL = 30 * 24 * time.Hour

// HashSessionToken 把明文会话 token 变成库内存储形态（SHA-256），防拖库后冒用。
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateUserSession(ctx context.Context, token string, userID int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_sessions(token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		HashSessionToken(token), userID, unixMilli(now), unixMilli(now.Add(userSessionTTL)))
	return err
}

// UserSessionUser 校验会话并返回用户。会话不存在或已过期返回 false。
func (s *Store) UserSessionUser(ctx context.Context, token string, now time.Time) (User, bool, error) {
	var u User
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.provider, u.external_id, u.username, u.name, u.avatar_url, u.trust_level, u.created_at
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`, HashSessionToken(token), unixMilli(now)).
		Scan(&u.ID, &u.Provider, &u.ExternalID, &u.Username, &u.Name, &u.AvatarURL, &u.TrustLevel, &created)
	if err != nil {
		return User{}, false, nil
	}
	u.CreatedAt = time.UnixMilli(created).UTC()
	return u, true, nil
}

func (s *Store) DeleteUserSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE token_hash = ?`, HashSessionToken(token))
	return err
}

func (s *Store) CleanupUserSessions(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE expires_at <= ?`, unixMilli(now))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
