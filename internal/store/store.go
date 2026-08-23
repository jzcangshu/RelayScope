package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"relaypulse/internal/domain"
	"relaypulse/internal/matcher"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	db *sql.DB
}

type Site struct {
	ID                  int64                   `json:"id"`
	Name                string                  `json:"name"`
	BaseURL             string                  `json:"baseUrl"`
	SourceURL           string                  `json:"sourceUrl"`
	AdapterKey          string                  `json:"adapterKey"`
	AdapterConfig       string                  `json:"adapterConfig"`
	CustomFailureReason string                  `json:"customFailureReason"`
	Enabled             bool                    `json:"enabled"`
	SessionRequired     bool                    `json:"sessionRequired"`
	Interval            time.Duration           `json:"interval"`
	Jitter              time.Duration           `json:"jitter"`
	IntervalSeconds     int64                   `json:"intervalSeconds"`
	JitterSeconds       int64                   `json:"jitterSeconds"`
	SessionConfigured   bool                    `json:"sessionConfigured"`
	AcquisitionState    domain.AcquisitionState `json:"acquisitionState"`
	CreatedAt           time.Time               `json:"createdAt"`
	UpdatedAt           time.Time               `json:"updatedAt"`
}

func (store *Store) UpdateSite(ctx context.Context, siteID int64, name, adapterKey, adapterConfig string, enabled bool, sessionRequired *bool, interval, jitter time.Duration) error {
	if siteID <= 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(adapterKey) == "" || interval < 5*time.Minute || jitter < 0 {
		return errors.New("invalid site update")
	}
	if strings.TrimSpace(adapterConfig) == "" {
		adapterConfig = "{}"
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin site update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE sites SET name = ?, adapter_key = ?, adapter_config = ?, enabled = ?, session_required = COALESCE(?, session_required), interval_seconds = ?, jitter_seconds = ?, updated_at = ? WHERE id = ?`, name, adapterKey, adapterConfig, boolInt(enabled), optionalBoolInt(sessionRequired), int64(interval/time.Second), int64(jitter/time.Second), unixMilli(time.Now().UTC()), siteID)
	if err != nil {
		return fmt.Errorf("update site: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("site not found")
	}
	if _, err := incrementRevision(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit site update: %w", err)
	}
	return nil
}

func (store *Store) UpdateSiteFailureReason(ctx context.Context, siteID int64, reason string) error {
	if siteID <= 0 {
		return errors.New("invalid site ID")
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > 500 {
		return errors.New("custom failure reason is too long")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin failure reason update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE sites SET custom_failure_reason = ?, updated_at = ? WHERE id = ?`, reason, unixMilli(time.Now().UTC()), siteID)
	if err != nil {
		return fmt.Errorf("update failure reason: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("site not found")
	}
	if _, err := incrementRevision(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failure reason update: %w", err)
	}
	return nil
}

type FailureAnnouncement struct {
	SiteID      int64  `json:"siteId"`
	SiteName    string `json:"siteName"`
	FailureCode string `json:"failureCode"`
	Reason      string `json:"reason"`
}

func (store *Store) ListActiveFailureAnnouncements(ctx context.Context) ([]FailureAnnouncement, error) {
	rows, err := store.db.QueryContext(ctx, `
		WITH latest_failed AS (
			SELECT site_id, COALESCE(error_code, '') AS error_code, COALESCE(error_message, '') AS error_message,
				ROW_NUMBER() OVER (PARTITION BY site_id ORDER BY started_at DESC, id DESC) AS site_rank
			FROM collection_runs WHERE status = 'failed'
		)
		SELECT site.id, site.name, site.acquisition_state, site.custom_failure_reason,
			COALESCE(latest_failed.error_code, ''), COALESCE(latest_failed.error_message, '')
		FROM sites site
		LEFT JOIN latest_failed ON latest_failed.site_id = site.id AND latest_failed.site_rank = 1
		WHERE site.enabled = 1 AND site.acquisition_state IN ('collection_failed', 'login_expired', 'challenge_pending', 'challenge_failed')
		ORDER BY site.name COLLATE NOCASE, site.id`)
	if err != nil {
		return nil, fmt.Errorf("list active failure announcements: %w", err)
	}
	defer rows.Close()
	items := make([]FailureAnnouncement, 0)
	for rows.Next() {
		var item FailureAnnouncement
		var state, customReason, errorCode, errorMessage string
		if err := rows.Scan(&item.SiteID, &item.SiteName, &state, &customReason, &errorCode, &errorMessage); err != nil {
			return nil, fmt.Errorf("scan failure announcement: %w", err)
		}
		item.FailureCode = strings.TrimSpace(errorCode)
		if item.FailureCode == "" {
			item.FailureCode = state
		}
		item.Reason = strings.TrimSpace(customReason)
		if item.Reason == "" {
			item.Reason = strings.TrimSpace(errorMessage)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate failure announcements: %w", err)
	}
	return items, nil
}

type CollectionRun struct {
	ID              int64      `json:"id"`
	SiteID          int64      `json:"siteId"`
	SiteName        string     `json:"siteName"`
	AdapterKey      string     `json:"adapterKey"`
	StartedAt       time.Time  `json:"startedAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	Status          string     `json:"status"`
	CatalogComplete bool       `json:"catalogComplete"`
	ModelsSeen      int        `json:"modelsSeen"`
	GroupsSeen      int        `json:"groupsSeen"`
	ErrorCode       string     `json:"errorCode,omitempty"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
}

func (store *Store) ListCollectionRuns(ctx context.Context, limit int) ([]CollectionRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT run.id, run.site_id, site.name, run.adapter_key, run.started_at, run.finished_at, run.status,
			run.catalog_complete, run.models_seen, run.groups_seen, COALESCE(run.error_code, ''), COALESCE(run.error_message, '')
		FROM collection_runs run JOIN sites site ON site.id = run.site_id
		ORDER BY run.started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list collection runs: %w", err)
	}
	defer rows.Close()
	runs := make([]CollectionRun, 0, limit)
	for rows.Next() {
		var run CollectionRun
		var started int64
		var finished sql.NullInt64
		var complete int
		if err := rows.Scan(&run.ID, &run.SiteID, &run.SiteName, &run.AdapterKey, &started, &finished, &run.Status, &complete, &run.ModelsSeen, &run.GroupsSeen, &run.ErrorCode, &run.ErrorMessage); err != nil {
			return nil, fmt.Errorf("scan collection run: %w", err)
		}
		run.StartedAt = time.UnixMilli(started).UTC()
		if finished.Valid {
			value := time.UnixMilli(finished.Int64).UTC()
			run.FinishedAt = &value
		}
		run.CatalogComplete = complete == 1
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collection runs: %w", err)
	}
	return runs, nil
}

func (store *Store) ListRecentCollectionRunsBySite(ctx context.Context, perSite int) ([]CollectionRun, error) {
	if perSite <= 0 || perSite > 50 {
		perSite = 12
	}
	rows, err := store.db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT run.id, run.site_id, site.name AS site_name, run.adapter_key, run.started_at,
				run.finished_at, run.status, run.catalog_complete, run.models_seen, run.groups_seen,
				COALESCE(run.error_code, '') AS error_code,
				COALESCE(run.error_message, '') AS error_message,
				ROW_NUMBER() OVER (PARTITION BY run.site_id ORDER BY run.started_at DESC, run.id DESC) AS site_rank
			FROM collection_runs run
			JOIN sites site ON site.id = run.site_id
		)
		SELECT id, site_id, site_name, adapter_key, started_at, finished_at, status,
			catalog_complete, models_seen, groups_seen, error_code, error_message
		FROM ranked
		WHERE site_rank <= ?
		ORDER BY site_name COLLATE NOCASE, started_at DESC, id DESC`, perSite)
	if err != nil {
		return nil, fmt.Errorf("list recent collection runs by site: %w", err)
	}
	defer rows.Close()
	runs := make([]CollectionRun, 0, perSite*8)
	for rows.Next() {
		var run CollectionRun
		var started int64
		var finished sql.NullInt64
		var complete int
		if err := rows.Scan(&run.ID, &run.SiteID, &run.SiteName, &run.AdapterKey, &started, &finished, &run.Status, &complete, &run.ModelsSeen, &run.GroupsSeen, &run.ErrorCode, &run.ErrorMessage); err != nil {
			return nil, fmt.Errorf("scan recent collection run: %w", err)
		}
		run.StartedAt = time.UnixMilli(started).UTC()
		if finished.Valid {
			value := time.UnixMilli(finished.Int64).UTC()
			run.FinishedAt = &value
		}
		run.CatalogComplete = complete == 1
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent collection runs: %w", err)
	}
	return runs, nil
}

type EncryptedSession struct {
	SiteID     int64
	Purpose    string
	KeyVersion int
	Nonce      []byte
	Ciphertext []byte
	ExpiresAt  *time.Time
	UpdatedAt  time.Time
}

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

func (store *Store) ListRuleNames(ctx context.Context) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT canonical_name FROM model_rules ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list rule names: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan rule name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule names: %w", err)
	}
	return names, nil
}

func (store *Store) CreateRule(ctx context.Context, rule matcher.Rule) error {
	if _, err := matcher.New([]matcher.Rule{rule}); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO model_rules(provider, canonical_name, required_tokens, any_tokens, excluded_tokens, aliases, pattern, priority, enabled, generated, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.Provider, rule.CanonicalName, encodeJSONStrings(rule.RequiredTerms), encodeJSONStrings(rule.AnyTerms), encodeJSONStrings(rule.ExcludedTerms), encodeJSONStrings(rule.Aliases), nullableString(rule.Pattern), rule.Priority, boolInt(rule.Enabled), boolInt(rule.Generated), unixMilli(now), unixMilli(now))
	if err != nil {
		return fmt.Errorf("create model rule %q: %w", rule.CanonicalName, err)
	}
	return nil
}

func (store *Store) UpdateRule(ctx context.Context, id int64, rule matcher.Rule) error {
	if id <= 0 || strings.TrimSpace(rule.Provider) == "" || strings.TrimSpace(rule.CanonicalName) == "" {
		return errors.New("invalid model rule")
	}
	if _, err := matcher.New([]matcher.Rule{rule}); err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE model_rules SET provider=?, canonical_name=?, required_tokens=?, any_tokens=?, excluded_tokens=?, aliases=?, pattern=?, priority=?, enabled=?, generated=?, updated_at=? WHERE id=?`,
		rule.Provider, rule.CanonicalName, encodeJSONStrings(rule.RequiredTerms), encodeJSONStrings(rule.AnyTerms), encodeJSONStrings(rule.ExcludedTerms), encodeJSONStrings(rule.Aliases), nullableString(rule.Pattern), rule.Priority, boolInt(rule.Enabled), boolInt(rule.Generated), unixMilli(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("update model rule: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("model rule not found")
	}
	return nil
}

func (store *Store) DeleteRule(ctx context.Context, id int64) error {
	result, err := store.db.ExecContext(ctx, `DELETE FROM model_rules WHERE id = ? AND generated = 0`, id)
	if err != nil {
		return fmt.Errorf("delete model rule: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("model rule not found or generated")
	}
	return nil
}

type MatchPreviewRow struct {
	SiteName     string `json:"siteName"`
	RawModelName string `json:"rawModelName"`
	Matched      bool   `json:"matched"`
}

type MatchConflict struct {
	SiteName       string   `json:"siteName"`
	RawModelName   string   `json:"rawModelName"`
	CandidateRules []string `json:"candidateRules"`
}

func (store *Store) ListMatchConflicts(ctx context.Context, limit int) ([]MatchConflict, error) {
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT sites.name, raw.raw_name, rule.canonical_name
		FROM raw_models raw
		JOIN sites ON sites.id = raw.site_id
		JOIN model_matches match ON match.raw_model_id = raw.id
		JOIN model_rules rule ON rule.id = match.rule_id
		WHERE raw.removed_at IS NULL AND raw.id IN (
			SELECT raw_model_id FROM model_matches GROUP BY raw_model_id HAVING COUNT(*) > 1
		)
		ORDER BY sites.name, raw.raw_name, rule.priority DESC, rule.canonical_name
		LIMIT ?`, limit*8)
	if err != nil {
		return nil, fmt.Errorf("list match conflicts: %w", err)
	}
	defer rows.Close()
	conflicts := make([]MatchConflict, 0)
	for rows.Next() {
		var siteName, rawName, ruleName string
		if err := rows.Scan(&siteName, &rawName, &ruleName); err != nil {
			return nil, fmt.Errorf("scan match conflict: %w", err)
		}
		if len(conflicts) == 0 || conflicts[len(conflicts)-1].SiteName != siteName || conflicts[len(conflicts)-1].RawModelName != rawName {
			if len(conflicts) >= limit {
				break
			}
			conflicts = append(conflicts, MatchConflict{SiteName: siteName, RawModelName: rawName})
		}
		last := &conflicts[len(conflicts)-1]
		last.CandidateRules = append(last.CandidateRules, ruleName)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate match conflicts: %w", err)
	}
	return conflicts, nil
}

func (store *Store) PreviewRule(ctx context.Context, rule matcher.Rule, limit int) ([]MatchPreviewRow, error) {
	engine, err := matcher.New([]matcher.Rule{rule})
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	rows, err := store.db.QueryContext(ctx, `SELECT sites.name, raw_models.raw_name FROM raw_models JOIN sites ON sites.id = raw_models.site_id WHERE raw_models.removed_at IS NULL ORDER BY sites.name, raw_models.raw_name LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]MatchPreviewRow, 0)
	for rows.Next() {
		var row MatchPreviewRow
		if err := rows.Scan(&row.SiteName, &row.RawModelName); err != nil {
			return nil, err
		}
		row.Matched = len(engine.Preview(row.RawModelName).Matches) > 0
		if row.Matched {
			result = append(result, row)
		}
	}
	return result, rows.Err()
}

func (store *Store) ListRules(ctx context.Context) ([]matcher.Rule, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, provider, canonical_name, required_tokens, any_tokens, excluded_tokens, aliases, COALESCE(pattern, ''), priority, enabled, generated FROM model_rules ORDER BY priority DESC, canonical_name`)
	if err != nil {
		return nil, fmt.Errorf("list model rules: %w", err)
	}
	defer rows.Close()
	var rules []matcher.Rule
	for rows.Next() {
		var rule matcher.Rule
		var requiredJSON, anyJSON, excludedJSON, aliasesJSON string
		var enabled, generated int
		if err := rows.Scan(&rule.ID, &rule.Provider, &rule.CanonicalName, &requiredJSON, &anyJSON, &excludedJSON, &aliasesJSON, &rule.Pattern, &rule.Priority, &enabled, &generated); err != nil {
			return nil, fmt.Errorf("scan model rule: %w", err)
		}
		if err := json.Unmarshal([]byte(requiredJSON), &rule.RequiredTerms); err != nil {
			return nil, fmt.Errorf("decode required terms: %w", err)
		}
		if err := json.Unmarshal([]byte(anyJSON), &rule.AnyTerms); err != nil {
			return nil, fmt.Errorf("decode any terms: %w", err)
		}
		if err := json.Unmarshal([]byte(excludedJSON), &rule.ExcludedTerms); err != nil {
			return nil, fmt.Errorf("decode excluded terms: %w", err)
		}
		if err := json.Unmarshal([]byte(aliasesJSON), &rule.Aliases); err != nil {
			return nil, fmt.Errorf("decode aliases: %w", err)
		}
		rule.Enabled, rule.Generated = enabled == 1, generated == 1
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model rules: %w", err)
	}
	return rules, nil
}

func (store *Store) RefreshMatches(ctx context.Context, engine *matcher.Engine, now time.Time) error {
	if engine == nil {
		return errors.New("matcher engine is required")
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id, raw_name FROM raw_models WHERE removed_at IS NULL`)
	if err != nil {
		return fmt.Errorf("list raw models for matching: %w", err)
	}
	type rawModel struct {
		id   int64
		name string
	}
	var rawModels []rawModel
	for rows.Next() {
		var raw rawModel
		if err := rows.Scan(&raw.id, &raw.name); err != nil {
			rows.Close()
			return fmt.Errorf("scan raw model for matching: %w", err)
		}
		rawModels = append(rawModels, raw)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate raw models for matching: %w", err)
	}
	rows.Close()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin match refresh: %w", err)
	}
	defer tx.Rollback()
	for _, raw := range rawModels {
		if _, err := tx.ExecContext(ctx, `DELETE FROM model_matches WHERE raw_model_id = ?`, raw.id); err != nil {
			return fmt.Errorf("clear matches for raw model %d: %w", raw.id, err)
		}
		preview := engine.Preview(raw.name)
		for _, match := range preview.Matches {
			if _, err := tx.ExecContext(ctx, `INSERT INTO model_matches(raw_model_id, rule_id, is_primary, explanation, matched_at) VALUES (?, ?, ?, ?, ?)`, raw.id, match.Rule.ID, boolInt(match.Primary), match.Explanation, unixMilli(now)); err != nil {
				return fmt.Errorf("write match for raw model %d: %w", raw.id, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit match refresh: %w", err)
	}
	return nil
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" {
		path = filepath.Clean(path)
	}

	params := url.Values{}
	params.Set("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "foreign_keys(1)")
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "synchronous(NORMAL)")
	dsn := "file:" + filepath.ToSlash(path) + "?" + params.Encode()

	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(2)
	database.SetConnMaxLifetime(0)

	store := &Store{db: database}
	if err := store.migrate(ctx); err != nil {
		database.Close()
		return nil, err
	}
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping SQLite: %w", err)
	}
	return store, nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

func (store *Store) DB() *sql.DB {
	return store.db
}

func (store *Store) migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	// Migrations are ordered, recorded, and applied transactionally.  The old
	// table-exists heuristic could silently skip a later migration and leave
	// schema_version lying about the actual database shape.
	var current int
	var versionText string
	versionErr := store.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = 'schema_version'`).Scan(&versionText)
	if versionErr == nil {
		parsed, parseErr := strconv.Atoi(versionText)
		if parseErr != nil || parsed < 0 {
			return fmt.Errorf("parse schema version %q: %w", versionText, parseErr)
		}
		current = parsed
	} else if !strings.Contains(strings.ToLower(versionErr.Error()), "no such table") && !errors.Is(versionErr, sql.ErrNoRows) {
		return fmt.Errorf("read schema version: %w", versionErr)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}
		content, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_meta(key, value) VALUES ('schema_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
		current = version
	}
	return nil
}

func migrationVersion(name string) (int, error) {
	underscore := strings.IndexByte(name, '_')
	if underscore <= 0 {
		return 0, fmt.Errorf("migration %q has no numeric prefix", name)
	}
	version, err := strconv.Atoi(name[:underscore])
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %q has invalid version", name)
	}
	return version, nil
}

func (store *Store) CreateSite(ctx context.Context, site Site) (Site, error) {
	if site.Name == "" || site.BaseURL == "" || site.SourceURL == "" || site.AdapterKey == "" {
		return Site{}, errors.New("site name, URLs, and adapter key are required")
	}
	if site.Interval == 0 {
		site.Interval = 15 * time.Minute
	}
	if site.Interval < 5*time.Minute || site.Jitter < 0 {
		return Site{}, errors.New("site interval must be at least five minutes and jitter cannot be negative")
	}
	if site.AdapterConfig == "" {
		site.AdapterConfig = "{}"
	}
	site.CustomFailureReason = strings.TrimSpace(site.CustomFailureReason)
	if len([]rune(site.CustomFailureReason)) > 500 {
		return Site{}, errors.New("custom failure reason is too long")
	}
	if !site.AcquisitionState.Valid() {
		site.AcquisitionState = domain.AcquisitionStale
	}
	now := time.Now().UTC()
	result, err := store.db.ExecContext(ctx, `
		INSERT INTO sites(name, base_url, source_url, adapter_key, adapter_config, custom_failure_reason, enabled, session_required,
			interval_seconds, jitter_seconds, acquisition_state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		site.Name, site.BaseURL, site.SourceURL, site.AdapterKey, site.AdapterConfig, site.CustomFailureReason, boolInt(site.Enabled),
		boolInt(site.SessionRequired), int64(site.Interval/time.Second), int64(site.Jitter/time.Second), site.AcquisitionState, unixMilli(now), unixMilli(now),
	)
	if err != nil {
		return Site{}, fmt.Errorf("create site: %w", err)
	}
	site.ID, err = result.LastInsertId()
	if err != nil {
		return Site{}, fmt.Errorf("read site ID: %w", err)
	}
	site.CreatedAt, site.UpdatedAt = now, now
	site.IntervalSeconds = int64(site.Interval / time.Second)
	site.JitterSeconds = int64(site.Jitter / time.Second)
	return site, nil
}

func (store *Store) CreateManagedSite(ctx context.Context, site Site) (Site, error) {
	return store.CreateSite(ctx, site)
}

func (store *Store) ListEnabledSites(ctx context.Context) ([]Site, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT id, name, base_url, source_url, adapter_key, adapter_config, custom_failure_reason, enabled, session_required,
			interval_seconds, jitter_seconds, acquisition_state, created_at, updated_at,
			EXISTS (SELECT 1 FROM encrypted_sessions WHERE site_id = sites.id AND purpose = 'site-http')
		FROM sites WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list enabled sites: %w", err)
	}
	defer rows.Close()

	var sites []Site
	for rows.Next() {
		var site Site
		var enabled, sessionRequired int
		var intervalSeconds, jitterSeconds int64
		var acquisitionState string
		var createdAt, updatedAt int64
		var sessionConfigured bool
		if err := rows.Scan(&site.ID, &site.Name, &site.BaseURL, &site.SourceURL, &site.AdapterKey, &site.AdapterConfig, &site.CustomFailureReason,
			&enabled, &sessionRequired, &intervalSeconds, &jitterSeconds, &acquisitionState, &createdAt, &updatedAt, &sessionConfigured); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		site.Enabled = enabled == 1
		site.SessionRequired = sessionRequired == 1
		site.Interval = time.Duration(intervalSeconds) * time.Second
		site.Jitter = time.Duration(jitterSeconds) * time.Second
		site.IntervalSeconds = intervalSeconds
		site.JitterSeconds = jitterSeconds
		site.AcquisitionState = domain.AcquisitionState(acquisitionState)
		site.SessionConfigured = sessionConfigured
		site.CreatedAt = time.UnixMilli(createdAt).UTC()
		site.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sites: %w", err)
	}
	return sites, nil
}

func (store *Store) ListAllSites(ctx context.Context) ([]Site, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT id, name, base_url, source_url, adapter_key, adapter_config, custom_failure_reason, enabled, session_required,
			interval_seconds, jitter_seconds, acquisition_state, created_at, updated_at,
			EXISTS (SELECT 1 FROM encrypted_sessions WHERE site_id = sites.id AND purpose = 'site-http')
		FROM sites ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list all sites: %w", err)
	}
	defer rows.Close()
	return scanSites(rows)
}

func (store *Store) StartCollectionRun(ctx context.Context, siteID int64, adapterKey string, startedAt time.Time) (int64, error) {
	result, err := store.db.ExecContext(ctx, `
		INSERT INTO collection_runs(site_id, adapter_key, started_at, status)
		VALUES (?, ?, ?, 'running')`, siteID, adapterKey, unixMilli(startedAt))
	if err != nil {
		return 0, fmt.Errorf("start collection run: %w", err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read collection run ID: %w", err)
	}
	return runID, nil
}

func (store *Store) FinishCollectionRun(ctx context.Context, runID int64, status string, complete bool, models, groups int, code, message string, finishedAt time.Time) error {
	if runID <= 0 || strings.TrimSpace(status) == "" {
		return errors.New("run ID and status are required")
	}
	_, err := store.db.ExecContext(ctx, `
		UPDATE collection_runs
		SET finished_at = ?, status = ?, catalog_complete = ?, models_seen = ?, groups_seen = ?, error_code = ?, error_message = ?
		WHERE id = ?`, unixMilli(finishedAt), status, boolInt(complete), models, groups, nullableString(code), nullableString(message), runID)
	if err != nil {
		return fmt.Errorf("finish collection run: %w", err)
	}
	return nil
}

func (store *Store) SetAcquisitionState(ctx context.Context, siteID int64, state domain.AcquisitionState, updatedAt time.Time) error {
	if !state.Valid() {
		return fmt.Errorf("invalid acquisition state %q", state)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin acquisition state update: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET acquisition_state = ?, updated_at = ? WHERE id = ?`, state, unixMilli(updatedAt), siteID); err != nil {
		return fmt.Errorf("set acquisition state: %w", err)
	}
	if state != domain.AcquisitionCollecting {
		if _, err := incrementRevision(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit acquisition state: %w", err)
	}
	return nil
}

func (store *Store) BumpRevision(ctx context.Context) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin revision update: %w", err)
	}
	defer tx.Rollback()
	if _, err := incrementRevision(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revision update: %w", err)
	}
	return nil
}

type siteRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanSites(rows siteRows) ([]Site, error) {
	var sites []Site
	for rows.Next() {
		var site Site
		var enabled, sessionRequired int
		var intervalSeconds, jitterSeconds int64
		var acquisitionState string
		var createdAt, updatedAt int64
		var sessionConfigured bool
		if err := rows.Scan(&site.ID, &site.Name, &site.BaseURL, &site.SourceURL, &site.AdapterKey, &site.AdapterConfig, &site.CustomFailureReason,
			&enabled, &sessionRequired, &intervalSeconds, &jitterSeconds, &acquisitionState, &createdAt, &updatedAt, &sessionConfigured); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		site.Enabled = enabled == 1
		site.SessionRequired = sessionRequired == 1
		site.Interval = time.Duration(intervalSeconds) * time.Second
		site.Jitter = time.Duration(jitterSeconds) * time.Second
		site.IntervalSeconds = intervalSeconds
		site.JitterSeconds = jitterSeconds
		site.AcquisitionState = domain.AcquisitionState(acquisitionState)
		site.SessionConfigured = sessionConfigured
		site.CreatedAt = time.UnixMilli(createdAt).UTC()
		site.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sites: %w", err)
	}
	return sites, nil
}

func (store *Store) ApplyCollection(ctx context.Context, collection domain.Collection, normalize func(string) string) (int64, error) {
	if err := collection.Validate(); err != nil {
		return 0, fmt.Errorf("validate collection: %w", err)
	}
	if normalize == nil {
		normalize = strings.ToLower
	}
	if collection.CatalogComplete && len(collection.CatalogRawNames) == 0 && len(collection.Models) > 0 {
		collection.CatalogRawNames = make([]string, 0, len(collection.Models))
		for _, model := range collection.Models {
			collection.CatalogRawNames = append(collection.CatalogRawNames, model.RawName)
		}
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin collection transaction: %w", err)
	}
	defer tx.Rollback()

	for _, model := range collection.Models {
		modelID, err := upsertRawModel(ctx, tx, collection.SiteID, model, collection.CollectedAt, normalize)
		if err != nil {
			return 0, err
		}
		for _, group := range model.Groups {
			groupID, err := upsertGroup(ctx, tx, modelID, group, collection.CollectedAt)
			if err != nil {
				return 0, err
			}
			if err := upsertSnapshot(ctx, tx, groupID, collection.RunID, collection, group); err != nil {
				return 0, err
			}
			for _, bucket := range group.Buckets {
				if err := upsertBucket(ctx, tx, groupID, collection.CollectedAt, bucket); err != nil {
					return 0, err
				}
			}
		}
	}

	if collection.CatalogComplete {
		if collection.MissingCatalogState != "" {
			if err := applyMissingCatalogState(ctx, tx, collection); err != nil {
				return 0, err
			}
		} else if err := updateAbsenceEvidence(ctx, tx, collection.SiteID, collection.CatalogRawNames, collection.CollectedAt); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET acquisition_state = ?, last_success_at = ?, updated_at = ? WHERE id = ?`,
		domain.AcquisitionFresh, unixMilli(collection.CollectedAt), unixMilli(collection.CollectedAt), collection.SiteID); err != nil {
		return 0, fmt.Errorf("update site success: %w", err)
	}

	revision, err := incrementRevision(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit collection: %w", err)
	}
	return revision, nil
}

func (store *Store) Revision(ctx context.Context) (int64, error) {
	var raw string
	if err := store.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = 'data_revision'`).Scan(&raw); err != nil {
		return 0, fmt.Errorf("read revision: %w", err)
	}
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse revision: %w", err)
	}
	return revision, nil
}

func (store *Store) Cleanup(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 || batchSize > 10_000 {
		return 0, errors.New("cleanup batch size must be between 1 and 10000")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin cleanup: %w", err)
	}
	defer tx.Rollback()

	var removed int64
	queries := []string{
		`DELETE FROM metric_buckets
		 WHERE (group_id, bucket_start, resolution_seconds) IN
		       (SELECT group_id, bucket_start, resolution_seconds FROM metric_buckets WHERE bucket_start < ? LIMIT ?)`,
		`DELETE FROM collection_runs WHERE id IN (SELECT id FROM collection_runs WHERE started_at < ? LIMIT ?)`,
	}
	for _, query := range queries {
		result, err := tx.ExecContext(ctx, query, unixMilli(cutoff), batchSize)
		if err != nil {
			return 0, fmt.Errorf("delete expired rows: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("count deleted rows: %w", err)
		}
		removed += count
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit cleanup: %w", err)
	}
	return removed, nil
}

func upsertRawModel(ctx context.Context, tx *sql.Tx, siteID int64, model domain.ModelObservation, collectedAt time.Time, normalize func(string) string) (int64, error) {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO raw_models(site_id, raw_name, normalized_name, provider_hint, source_extension, first_seen_at, last_seen_at,
			history_coverage_start, history_coverage_end)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(site_id, raw_name) DO UPDATE SET
			normalized_name = excluded.normalized_name,
			provider_hint = excluded.provider_hint,
			source_extension = excluded.source_extension,
			last_seen_at = excluded.last_seen_at,
			history_coverage_start = CASE
				WHEN excluded.history_coverage_start IS NULL OR excluded.history_coverage_end IS NULL THEN NULL
				WHEN raw_models.history_coverage_start IS NOT NULL
				 AND raw_models.history_coverage_end >= excluded.history_coverage_start
				 AND raw_models.history_coverage_start <= excluded.history_coverage_end
				THEN MIN(raw_models.history_coverage_start, excluded.history_coverage_start)
				ELSE excluded.history_coverage_start
			END,
			history_coverage_end = CASE
				WHEN excluded.history_coverage_start IS NULL OR excluded.history_coverage_end IS NULL THEN NULL
				WHEN raw_models.history_coverage_start IS NOT NULL
				 AND raw_models.history_coverage_end >= excluded.history_coverage_start
				 AND raw_models.history_coverage_start <= excluded.history_coverage_end
				THEN MAX(raw_models.history_coverage_end, excluded.history_coverage_end)
				ELSE excluded.history_coverage_end
			END,
			absent_complete_runs = 0,
			removed_at = NULL`,
		siteID, model.RawName, normalize(model.RawName), model.Provider, nullableJSON(model.Extension), unixMilli(collectedAt), unixMilli(collectedAt),
		nullableUnixMilli(model.HistoryCoverageStart), nullableUnixMilli(model.HistoryCoverageEnd),
	)
	if err != nil {
		return 0, fmt.Errorf("upsert raw model %q: %w", model.RawName, err)
	}
	var modelID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM raw_models WHERE site_id = ? AND raw_name = ?`, siteID, model.RawName).Scan(&modelID); err != nil {
		return 0, fmt.Errorf("read raw model %q: %w", model.RawName, err)
	}
	return modelID, nil
}

func upsertGroup(ctx context.Context, tx *sql.Tx, modelID int64, group domain.GroupObservation, collectedAt time.Time) (int64, error) {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO site_groups(raw_model_id, raw_name, source_extension, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(raw_model_id, raw_name) DO UPDATE SET
			source_extension = excluded.source_extension,
			last_seen_at = excluded.last_seen_at`,
		modelID, group.RawName, nullableJSON(group.Extension), unixMilli(collectedAt), unixMilli(collectedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("upsert group %q: %w", group.RawName, err)
	}
	var groupID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM site_groups WHERE raw_model_id = ? AND raw_name = ?`, modelID, group.RawName).Scan(&groupID); err != nil {
		return 0, fmt.Errorf("read group %q: %w", group.RawName, err)
	}
	return groupID, nil
}

func upsertSnapshot(ctx context.Context, tx *sql.Tx, groupID, runID int64, collection domain.Collection, group domain.GroupObservation) error {
	metrics := group.Metrics
	observedAt := group.ObservedAt
	if observedAt.IsZero() {
		observedAt = collection.ObservedAt
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO current_snapshots(group_id, run_id, service_state, observed_at, collected_at,
			request_count, success_count, failure_count, empty_count, success_ratio,
			average_latency_ms, first_token_ms, tokens_per_second, source_extension)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(group_id) DO UPDATE SET
			run_id = excluded.run_id, service_state = excluded.service_state,
			observed_at = excluded.observed_at, collected_at = excluded.collected_at,
			request_count = excluded.request_count, success_count = excluded.success_count,
			failure_count = excluded.failure_count, empty_count = excluded.empty_count,
			success_ratio = excluded.success_ratio, average_latency_ms = excluded.average_latency_ms,
			first_token_ms = excluded.first_token_ms, tokens_per_second = excluded.tokens_per_second,
			source_extension = excluded.source_extension`,
		groupID, nullableRunID(runID), group.ServiceState, unixMilli(observedAt), unixMilli(collection.CollectedAt),
		metrics.RequestCount, metrics.SuccessCount, metrics.FailureCount, metrics.EmptyCount, metrics.SuccessRatio,
		metrics.AverageLatencyMS, metrics.FirstTokenMS, metrics.TokensPerSecond, nullableJSON(group.Extension),
	)
	if err != nil {
		return fmt.Errorf("upsert current snapshot for group %d: %w", groupID, err)
	}
	return nil
}

func upsertBucket(ctx context.Context, tx *sql.Tx, groupID int64, collectedAt time.Time, bucket domain.TimeBucket) error {
	metrics := bucket.Metrics
	// A source may change its sampling resolution. Remove the old interval at
	// the same start so a detail view cannot show duplicate overlapping points.
	if _, err := tx.ExecContext(ctx, `DELETE FROM metric_buckets WHERE group_id = ? AND bucket_start = ? AND resolution_seconds <> ?`, groupID, unixMilli(bucket.Start), int64(bucket.Resolution/time.Second)); err != nil {
		return fmt.Errorf("remove superseded metric bucket for group %d: %w", groupID, err)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO metric_buckets(group_id, bucket_start, bucket_end, resolution_seconds,
			request_count, success_count, failure_count, empty_count, success_ratio,
			average_latency_ms, first_token_ms, tokens_per_second, collected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(group_id, bucket_start, resolution_seconds) DO UPDATE SET
			bucket_end = excluded.bucket_end, request_count = excluded.request_count,
			success_count = excluded.success_count, failure_count = excluded.failure_count,
			empty_count = excluded.empty_count, success_ratio = excluded.success_ratio,
			average_latency_ms = excluded.average_latency_ms, first_token_ms = excluded.first_token_ms,
			tokens_per_second = excluded.tokens_per_second, collected_at = excluded.collected_at`,
		groupID, unixMilli(bucket.Start), unixMilli(bucket.End), int64(bucket.Resolution/time.Second),
		metrics.RequestCount, metrics.SuccessCount, metrics.FailureCount, metrics.EmptyCount, metrics.SuccessRatio,
		metrics.AverageLatencyMS, metrics.FirstTokenMS, metrics.TokensPerSecond, unixMilli(collectedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert metric bucket for group %d: %w", groupID, err)
	}
	return nil
}

func updateAbsenceEvidence(ctx context.Context, tx *sql.Tx, siteID int64, catalogRawNames []string, collectedAt time.Time) error {
	if len(catalogRawNames) > 0 {
		placeholders := make([]string, len(catalogRawNames))
		args := make([]any, 2, len(catalogRawNames)+2)
		args[0] = unixMilli(collectedAt)
		args[1] = siteID
		for index, rawName := range catalogRawNames {
			placeholders[index] = "?"
			args = append(args, rawName)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE raw_models SET absent_complete_runs = 0, removed_at = NULL, last_seen_at = ? WHERE site_id = ? AND raw_name IN (`+strings.Join(placeholders, ",")+")", args...); err != nil {
			return fmt.Errorf("reset model absence evidence: %w", err)
		}
	}
	query := `UPDATE raw_models SET absent_complete_runs = absent_complete_runs + 1,
		removed_at = CASE WHEN absent_complete_runs + 1 >= 3 THEN COALESCE(removed_at, ?) ELSE removed_at END
		WHERE site_id = ?`
	args := []any{unixMilli(collectedAt), siteID}
	if len(catalogRawNames) > 0 {
		placeholders := make([]string, len(catalogRawNames))
		for index, rawName := range catalogRawNames {
			placeholders[index] = "?"
			args = append(args, rawName)
		}
		query += ` AND raw_name NOT IN (` + strings.Join(placeholders, ",") + `)`
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update model absence evidence: %w", err)
	}
	return nil
}

func applyMissingCatalogState(ctx context.Context, tx *sql.Tx, collection domain.Collection) error {
	query := `SELECT groups.id FROM site_groups groups
		JOIN raw_models raw ON raw.id = groups.raw_model_id
		WHERE raw.site_id = ? AND raw.removed_at IS NULL`
	args := []any{collection.SiteID}
	if len(collection.CatalogRawNames) > 0 {
		placeholders := make([]string, len(collection.CatalogRawNames))
		for index, rawName := range collection.CatalogRawNames {
			placeholders[index] = "?"
			args = append(args, rawName)
		}
		query += ` AND raw.raw_name NOT IN (` + strings.Join(placeholders, ",") + `)`
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query missing catalog groups: %w", err)
	}
	groupIDs := make([]int64, 0)
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			rows.Close()
			return fmt.Errorf("scan missing catalog group: %w", err)
		}
		groupIDs = append(groupIDs, groupID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close missing catalog groups: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate missing catalog groups: %w", err)
	}

	var ratio *float64
	switch collection.MissingCatalogState {
	case domain.ServiceHealthy:
		value := 1.0
		ratio = &value
	case domain.ServiceDegraded:
		value := 0.5
		ratio = &value
	case domain.ServiceFailed:
		value := 0.0
		ratio = &value
	}
	var intervalSeconds int64
	if err := tx.QueryRowContext(ctx, `SELECT interval_seconds FROM sites WHERE id = ?`, collection.SiteID).Scan(&intervalSeconds); err != nil {
		return fmt.Errorf("read site interval for missing catalog: %w", err)
	}
	resolution := time.Duration(intervalSeconds) * time.Second
	for _, groupID := range groupIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE current_snapshots SET
			run_id = ?, service_state = ?, observed_at = ?, collected_at = ?,
			request_count = NULL, success_count = NULL, failure_count = NULL, empty_count = NULL,
			success_ratio = ?, average_latency_ms = NULL, first_token_ms = NULL, tokens_per_second = NULL
			WHERE group_id = ?`, nullableRunID(collection.RunID), collection.MissingCatalogState,
			unixMilli(collection.ObservedAt), unixMilli(collection.CollectedAt), ratio, groupID); err != nil {
			return fmt.Errorf("update missing catalog snapshot for group %d: %w", groupID, err)
		}
		if ratio != nil {
			bucket := domain.TimeBucket{
				Start: collection.ObservedAt.Add(-resolution), End: collection.ObservedAt,
				Resolution: resolution, Metrics: domain.Metrics{SuccessRatio: ratio},
			}
			if err := upsertBucket(ctx, tx, groupID, collection.CollectedAt, bucket); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE raw_models SET absent_complete_runs = 0, removed_at = NULL WHERE site_id = ?`, collection.SiteID); err != nil {
		return fmt.Errorf("reset presence catalog absence evidence: %w", err)
	}
	return nil
}

func incrementRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE app_meta SET value = CAST(value AS INTEGER) + 1 WHERE key = 'data_revision'`); err != nil {
		return 0, fmt.Errorf("increment data revision: %w", err)
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = 'data_revision'`).Scan(&raw); err != nil {
		return 0, fmt.Errorf("read incremented revision: %w", err)
	}
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse incremented revision: %w", err)
	}
	return revision, nil
}

func unixMilli(value time.Time) int64 { return value.UTC().UnixMilli() }
func nullableUnixMilli(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return unixMilli(value)
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func optionalBoolInt(value *bool) any {
	if value == nil {
		return nil
	}
	return boolInt(*value)
}
func nullableRunID(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func encodeJSONStrings(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
