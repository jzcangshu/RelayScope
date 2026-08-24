package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"relayscope/internal/matcher"
)

func (store *Store) ListUnmatchedModels(ctx context.Context, limit int) ([]UnmatchedModel, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT raw.site_id, site.name, raw.raw_name, raw.provider_hint, raw.last_seen_at
		FROM raw_models raw JOIN sites site ON site.id = raw.site_id
		LEFT JOIN model_matches matches ON matches.raw_model_id = raw.id
		WHERE site.deleted_at IS NULL AND site.enabled = 1 AND raw.removed_at IS NULL AND matches.raw_model_id IS NULL
		ORDER BY raw.last_seen_at DESC, site.name COLLATE NOCASE, raw.raw_name LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list unmatched models: %w", err)
	}
	defer rows.Close()
	result := make([]UnmatchedModel, 0)
	for rows.Next() {
		var item UnmatchedModel
		var lastSeen int64
		if err := rows.Scan(&item.SiteID, &item.SiteName, &item.RawModelName, &item.ProviderHint, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan unmatched model: %w", err)
		}
		item.LastSeenAt = time.UnixMilli(lastSeen).UTC()
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unmatched models: %w", err)
	}
	return result, nil
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
		WHERE raw.removed_at IS NULL AND sites.deleted_at IS NULL AND raw.id IN (
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
	rows, err := store.db.QueryContext(ctx, `SELECT sites.name, raw_models.raw_name FROM raw_models JOIN sites ON sites.id = raw_models.site_id WHERE raw_models.removed_at IS NULL AND sites.deleted_at IS NULL ORDER BY sites.name, raw_models.raw_name LIMIT ?`, limit)
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

// RefreshAllMatches recomputes matches for every non-removed raw model.
// It runs after rule changes (ReloadMatcher), not after every collection.
func (store *Store) RefreshAllMatches(ctx context.Context, engine *matcher.Engine, now time.Time) error {
	if engine == nil {
		return errors.New("matcher engine is required")
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id, raw_name FROM raw_models WHERE removed_at IS NULL`)
	if err != nil {
		return fmt.Errorf("list raw models for matching: %w", err)
	}
	rawModels, err := scanRawMatchTargets(rows)
	if err != nil {
		return err
	}
	return store.refreshMatches(ctx, engine, rawModels, now)
}

// RefreshMatchesForRawModels recomputes matches only for the given raw models,
// keeping per-collection refresh cost proportional to the collected models.
func (store *Store) RefreshMatchesForRawModels(ctx context.Context, engine *matcher.Engine, rawModelIDs []int64, now time.Time) error {
	if engine == nil {
		return errors.New("matcher engine is required")
	}
	if len(rawModelIDs) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(rawModelIDs)), ",")
	args := make([]any, len(rawModelIDs))
	for index, id := range rawModelIDs {
		args[index] = id
	}
	rows, err := store.db.QueryContext(ctx, `SELECT id, raw_name FROM raw_models WHERE removed_at IS NULL AND id IN (`+placeholders+`)`, args...)
	if err != nil {
		return fmt.Errorf("list raw models for matching: %w", err)
	}
	rawModels, err := scanRawMatchTargets(rows)
	if err != nil {
		return err
	}
	return store.refreshMatches(ctx, engine, rawModels, now)
}

type rawMatchTarget struct {
	id   int64
	name string
}

func scanRawMatchTargets(rows *sql.Rows) ([]rawMatchTarget, error) {
	defer rows.Close()
	var rawModels []rawMatchTarget
	for rows.Next() {
		var raw rawMatchTarget
		if err := rows.Scan(&raw.id, &raw.name); err != nil {
			return nil, fmt.Errorf("scan raw model for matching: %w", err)
		}
		rawModels = append(rawModels, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate raw models for matching: %w", err)
	}
	return rawModels, nil
}

func (store *Store) refreshMatches(ctx context.Context, engine *matcher.Engine, rawModels []rawMatchTarget, now time.Time) error {
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
