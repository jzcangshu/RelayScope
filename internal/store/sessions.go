package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (store *Store) SaveEncryptedSession(ctx context.Context, session EncryptedSession) error {
	return store.SaveEncryptedSessions(ctx, []EncryptedSession{session})
}

func (store *Store) SaveEncryptedSessions(ctx context.Context, sessions []EncryptedSession) error {
	if len(sessions) == 0 {
		return errors.New("encrypted sessions are required")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin encrypted session batch: %w", err)
	}
	defer tx.Rollback()
	for _, session := range sessions {
		if session.SiteID <= 0 || strings.TrimSpace(session.Purpose) == "" || len(session.Nonce) == 0 || len(session.Ciphertext) == 0 {
			return errors.New("invalid encrypted session")
		}
		if session.KeyVersion <= 0 {
			session.KeyVersion = 1
		}
		if session.UpdatedAt.IsZero() {
			session.UpdatedAt = time.Now().UTC()
		}
		var expires any
		if session.ExpiresAt != nil {
			expires = unixMilli(*session.ExpiresAt)
		}
		if _, err := tx.ExecContext(ctx, `
		INSERT INTO encrypted_sessions(site_id, purpose, key_version, nonce, ciphertext, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(site_id, purpose) DO UPDATE SET key_version = excluded.key_version, nonce = excluded.nonce,
		ciphertext = excluded.ciphertext, expires_at = excluded.expires_at, updated_at = excluded.updated_at`,
			session.SiteID, session.Purpose, session.KeyVersion, session.Nonce, session.Ciphertext, expires, unixMilli(session.UpdatedAt)); err != nil {
			return fmt.Errorf("save encrypted session: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit encrypted session batch: %w", err)
	}
	return nil
}

func (store *Store) LoadEncryptedSession(ctx context.Context, siteID int64, purpose string) (EncryptedSession, error) {
	var session EncryptedSession
	var expires sql.NullInt64
	var updated int64
	if err := store.db.QueryRowContext(ctx, `SELECT site_id, purpose, key_version, nonce, ciphertext, expires_at, updated_at FROM encrypted_sessions WHERE site_id = ? AND purpose = ?`, siteID, purpose).
		Scan(&session.SiteID, &session.Purpose, &session.KeyVersion, &session.Nonce, &session.Ciphertext, &expires, &updated); err != nil {
		return EncryptedSession{}, fmt.Errorf("load encrypted session: %w", err)
	}
	if expires.Valid {
		value := time.UnixMilli(expires.Int64).UTC()
		session.ExpiresAt = &value
	}
	session.UpdatedAt = time.UnixMilli(updated).UTC()
	return session, nil
}

func (store *Store) DeleteEncryptedSession(ctx context.Context, siteID int64, purpose string) error {
	if _, err := store.db.ExecContext(ctx, `DELETE FROM encrypted_sessions WHERE site_id = ? AND purpose = ?`, siteID, purpose); err != nil {
		return fmt.Errorf("delete encrypted session: %w", err)
	}
	return nil
}

func (store *Store) SessionExpiresAt(ctx context.Context, siteID int64, purpose string) (*time.Time, bool, error) {
	var expires sql.NullInt64
	err := store.db.QueryRowContext(ctx, `SELECT expires_at FROM encrypted_sessions WHERE site_id = ? AND purpose = ?`, siteID, purpose).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("query session expiry: %w", err)
	}
	if !expires.Valid {
		return nil, true, nil
	}
	value := time.UnixMilli(expires.Int64).UTC()
	return &value, true, nil
}
