package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

type User struct {
	ID        int64     `json:"id"`
	LinuxDOID string    `json:"linuxdoId"`
	Username  string    `json:"username"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatarUrl"`
	CreatedAt time.Time `json:"createdAt"`
}

type Feedback struct {
	ID        int64     `json:"id"`
	User      User      `json:"user"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) UpsertUser(ctx context.Context, linuxdoID, username, name, avatarURL string) (User, error) {
	linuxdoID = strings.TrimSpace(linuxdoID)
	username = strings.TrimSpace(username)
	if linuxdoID == "" || username == "" || len(linuxdoID) > 200 || len(username) > 200 || len(name) > 200 || len(avatarURL) > 1000 {
		return User{}, errors.New("invalid user")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(linuxdo_id, username, name, avatar_url, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(linuxdo_id) DO UPDATE SET username=excluded.username, name=excluded.name, avatar_url=excluded.avatar_url, updated_at=excluded.updated_at`, linuxdoID, username, strings.TrimSpace(name), strings.TrimSpace(avatarURL), unixMilli(now), unixMilli(now))
	if err != nil {
		return User{}, err
	}
	return s.GetUserByLinuxDOID(ctx, linuxdoID)
}

func (s *Store) GetUserByLinuxDOID(ctx context.Context, id string) (User, error) {
	var u User
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, linuxdo_id, username, name, avatar_url, created_at FROM users WHERE linuxdo_id = ?`, strings.TrimSpace(id)).Scan(&u.ID, &u.LinuxDOID, &u.Username, &u.Name, &u.AvatarURL, &created)
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = time.UnixMilli(created).UTC()
	return u, nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	var u User
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, linuxdo_id, username, name, avatar_url, created_at FROM users WHERE id = ?`, id).Scan(&u.ID, &u.LinuxDOID, &u.Username, &u.Name, &u.AvatarURL, &created)
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
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.content, f.created_at, u.id, u.linuxdo_id, u.username, u.name, u.avatar_url, u.created_at FROM feedback f JOIN users u ON u.id=f.user_id ORDER BY f.created_at DESC, f.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Feedback, 0, limit)
	for rows.Next() {
		var f Feedback
		var created, userCreated int64
		if err := rows.Scan(&f.ID, &f.Content, &created, &f.User.ID, &f.User.LinuxDOID, &f.User.Username, &f.User.Name, &f.User.AvatarURL, &userCreated); err != nil {
			return nil, err
		}
		f.CreatedAt = time.UnixMilli(created).UTC()
		f.User.CreatedAt = time.UnixMilli(userCreated).UTC()
		items = append(items, f)
	}
	return items, rows.Err()
}
