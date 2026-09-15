package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Preferences 与公开页 localStorage 的三份定制数据同构，服务器只校验结构、不解释语义。
type Preferences struct {
	Hidden         PreferencesHidden         `json:"hidden"`
	DefaultHealthy bool                      `json:"defaultHealthy"`
	Tags           map[string]PreferencesTag `json:"tags"`
	UpdatedAt      time.Time                 `json:"updatedAt"`
}

type PreferencesHidden struct {
	Sites     []string `json:"sites"`
	Providers []string `json:"providers"`
	Models    []string `json:"models"`
}

type PreferencesTag struct {
	Color string   `json:"color"`
	Sites []string `json:"sites"`
}

var ErrPreferencesTooLarge = errors.New("偏好数据超出限制")

func defaultPreferences() Preferences {
	return Preferences{
		Hidden: PreferencesHidden{Sites: []string{}, Providers: []string{}, Models: []string{}},
		Tags:   map[string]PreferencesTag{},
	}
}

func validateStringList(list []string, maxItems, maxRune int) error {
	if len(list) > maxItems {
		return ErrPreferencesTooLarge
	}
	for _, item := range list {
		if len([]rune(item)) > maxRune {
			return ErrPreferencesTooLarge
		}
	}
	return nil
}

func (p Preferences) validate() error {
	if err := validateStringList(p.Hidden.Sites, 500, 200); err != nil {
		return err
	}
	if err := validateStringList(p.Hidden.Providers, 500, 200); err != nil {
		return err
	}
	if err := validateStringList(p.Hidden.Models, 500, 200); err != nil {
		return err
	}
	if len(p.Tags) > 100 {
		return ErrPreferencesTooLarge
	}
	for name, tag := range p.Tags {
		if len([]rune(name)) == 0 || len([]rune(name)) > 60 || len(tag.Color) > 20 {
			return ErrPreferencesTooLarge
		}
		if err := validateStringList(tag.Sites, 1000, 200); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetUserPreferences(ctx context.Context, userID int64) (Preferences, error) {
	prefs := defaultPreferences()
	var hidden, tags string
	var defaultHealthy, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT hidden, default_healthy, tags, updated_at FROM user_preferences WHERE user_id = ?`, userID).
		Scan(&hidden, &defaultHealthy, &tags, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prefs, nil
		}
		return Preferences{}, err
	}
	if err := json.Unmarshal([]byte(hidden), &prefs.Hidden); err != nil {
		return prefs, nil
	}
	if err := json.Unmarshal([]byte(tags), &prefs.Tags); err != nil {
		prefs.Tags = map[string]PreferencesTag{}
	}
	prefs.DefaultHealthy = defaultHealthy == 1
	if prefs.Hidden.Sites == nil {
		prefs.Hidden.Sites = []string{}
	}
	if prefs.Hidden.Providers == nil {
		prefs.Hidden.Providers = []string{}
	}
	if prefs.Hidden.Models == nil {
		prefs.Hidden.Models = []string{}
	}
	prefs.UpdatedAt = time.UnixMilli(updated).UTC()
	return prefs, nil
}

// PutUserPreferences 全量覆盖保存用户偏好；JSON 序列化失败或结构超限时报错。
func (s *Store) PutUserPreferences(ctx context.Context, userID int64, prefs Preferences) (Preferences, error) {
	if userID <= 0 {
		return Preferences{}, errors.New("invalid user")
	}
	if prefs.Hidden.Sites == nil {
		prefs.Hidden.Sites = []string{}
	}
	if prefs.Hidden.Providers == nil {
		prefs.Hidden.Providers = []string{}
	}
	if prefs.Hidden.Models == nil {
		prefs.Hidden.Models = []string{}
	}
	if prefs.Tags == nil {
		prefs.Tags = map[string]PreferencesTag{}
	}
	if err := prefs.validate(); err != nil {
		return Preferences{}, err
	}
	hidden, err := json.Marshal(prefs.Hidden)
	if err != nil {
		return Preferences{}, err
	}
	tags, err := json.Marshal(prefs.Tags)
	if err != nil {
		return Preferences{}, err
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO user_preferences(user_id, hidden, default_healthy, tags, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET hidden = excluded.hidden, default_healthy = excluded.default_healthy, tags = excluded.tags, updated_at = excluded.updated_at`,
		userID, string(hidden), boolToInt(prefs.DefaultHealthy), string(tags), unixMilli(now)); err != nil {
		return Preferences{}, err
	}
	prefs.UpdatedAt = now
	return prefs, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
