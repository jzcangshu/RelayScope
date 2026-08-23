package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

type User struct {
	ID         int64     `json:"id"`
	Provider   string    `json:"provider"`
	ExternalID string    `json:"externalId"`
	Username   string    `json:"username"`
	Name       string    `json:"name"`
	AvatarURL  string    `json:"avatarUrl"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Feedback struct {
	ID        int64     `json:"id"`
	User      User      `json:"user"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) UpsertUser(ctx context.Context, provider, externalID, username, name, avatarURL string) (User, error) {
	provider = strings.TrimSpace(provider)
	externalID = strings.TrimSpace(externalID)
	username = strings.TrimSpace(username)
	if provider == "" || externalID == "" || username == "" || len(provider) > 100 || len(externalID) > 200 || len(username) > 200 || len(name) > 200 || len(avatarURL) > 1000 {
		return User{}, errors.New("invalid user")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(provider, external_id, username, name, avatar_url, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(provider, external_id) DO UPDATE SET username=excluded.username, name=excluded.name, avatar_url=excluded.avatar_url, updated_at=excluded.updated_at`, provider, externalID, username, strings.TrimSpace(name), strings.TrimSpace(avatarURL), unixMilli(now), unixMilli(now))
	if err != nil {
		return User{}, err
	}
	return s.GetUserByExternalID(ctx, provider, externalID)
}

func (s *Store) GetUserByExternalID(ctx context.Context, provider, externalID string) (User, error) {
	var u User
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, provider, external_id, username, name, avatar_url, created_at FROM users WHERE provider = ? AND external_id = ?`, strings.TrimSpace(provider), strings.TrimSpace(externalID)).Scan(&u.ID, &u.Provider, &u.ExternalID, &u.Username, &u.Name, &u.AvatarURL, &created)
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = time.UnixMilli(created).UTC()
	return u, nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	var u User
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, provider, external_id, username, name, avatar_url, created_at FROM users WHERE id = ?`, id).Scan(&u.ID, &u.Provider, &u.ExternalID, &u.Username, &u.Name, &u.AvatarURL, &created)
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = time.UnixMilli(created).UTC()
	return u, nil
}

func (s *Store) CreateFeedback(ctx context.Context, userID int64, content string) error {
	content = strings.TrimSpace(content)
	if userID <= 0 || content == "" || len([]rune(content)) > 2000 {
		return errors.New("invalid feedback")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO feedback(user_id, content, created_at) VALUES (?, ?, ?)`, userID, content, unixMilli(time.Now().UTC()))
	return err
}

func (s *Store) ListFeedback(ctx context.Context, limit int) ([]Feedback, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.content, f.created_at, u.id, u.provider, u.external_id, u.username, u.name, u.avatar_url, u.created_at FROM feedback f JOIN users u ON u.id=f.user_id ORDER BY f.created_at DESC, f.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Feedback, 0, limit)
	for rows.Next() {
		var f Feedback
		var created, userCreated int64
		if err := rows.Scan(&f.ID, &f.Content, &created, &f.User.ID, &f.User.Provider, &f.User.ExternalID, &f.User.Username, &f.User.Name, &f.User.AvatarURL, &userCreated); err != nil {
			return nil, err
		}
		f.CreatedAt = time.UnixMilli(created).UTC()
		f.User.CreatedAt = time.UnixMilli(userCreated).UTC()
		items = append(items, f)
	}
	return items, rows.Err()
}
