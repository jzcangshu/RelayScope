package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"relaypulse/internal/domain"
)

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
	result, err := tx.ExecContext(ctx, `UPDATE sites SET name = ?, adapter_key = ?, adapter_config = ?, enabled = ?, session_required = COALESCE(?, session_required), interval_seconds = ?, jitter_seconds = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, name, adapterKey, adapterConfig, boolInt(enabled), optionalBoolInt(sessionRequired), int64(interval/time.Second), int64(jitter/time.Second), unixMilli(time.Now().UTC()), siteID)
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

// UpdateSiteDetails applies all editable site fields in one transaction so a
// malformed URL or failure reason cannot leave a partially updated site.
func (store *Store) UpdateSiteDetails(ctx context.Context, siteID int64, name, baseURL, sourceURL, adapterKey, adapterConfig string, enabled bool, sessionRequired bool, interval, jitter time.Duration, failureReason string) error {
	if siteID <= 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(adapterKey) == "" || interval < 5*time.Minute || jitter < 0 {
		return errors.New("invalid site update")
	}
	baseURL, sourceURL = strings.TrimSpace(baseURL), strings.TrimSpace(sourceURL)
	if err := validateSiteURL("base URL", baseURL); err != nil {
		return err
	}
	if err := validateSiteURL("source URL", sourceURL); err != nil {
		return err
	}
	if strings.TrimSpace(adapterConfig) == "" {
		adapterConfig = "{}"
	}
	failureReason = strings.TrimSpace(failureReason)
	if len([]rune(failureReason)) > 500 {
		return errors.New("custom failure reason is too long")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin site details update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE sites SET name = ?, base_url = ?, source_url = ?, adapter_key = ?, adapter_config = ?, custom_failure_reason = ?, enabled = ?, session_required = ?, interval_seconds = ?, jitter_seconds = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		name, baseURL, sourceURL, adapterKey, adapterConfig, failureReason, boolInt(enabled), boolInt(sessionRequired), int64(interval/time.Second), int64(jitter/time.Second), unixMilli(time.Now().UTC()), siteID)
	if err != nil {
		return fmt.Errorf("update site details: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("site not found")
	}
	if _, err := incrementRevision(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit site details update: %w", err)
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
	result, err := tx.ExecContext(ctx, `UPDATE sites SET custom_failure_reason = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, reason, unixMilli(time.Now().UTC()), siteID)
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
		WHERE site.enabled = 1 AND site.deleted_at IS NULL AND site.acquisition_state IN ('collection_failed', 'login_expired', 'challenge_pending', 'challenge_failed')
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

func (store *Store) CreateSite(ctx context.Context, site Site) (Site, error) {
	if site.Name == "" || site.BaseURL == "" || site.SourceURL == "" || site.AdapterKey == "" {
		return Site{}, errors.New("site name, URLs, and adapter key are required")
	}
	for name, value := range map[string]string{"base URL": site.BaseURL, "source URL": site.SourceURL} {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return Site{}, fmt.Errorf("invalid %s", name)
		}
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
			interval_seconds, jitter_seconds, acquisition_state, next_run_at, deleted_at, created_at, updated_at,
			EXISTS (SELECT 1 FROM encrypted_sessions WHERE site_id = sites.id AND purpose = 'site-http')
		FROM sites WHERE enabled = 1 AND deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list enabled sites: %w", err)
	}
	defer rows.Close()
	return scanSites(rows)
}

func (store *Store) ListAllSites(ctx context.Context) ([]Site, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT id, name, base_url, source_url, adapter_key, adapter_config, custom_failure_reason, enabled, session_required,
			interval_seconds, jitter_seconds, acquisition_state, next_run_at, deleted_at, created_at, updated_at,
			EXISTS (SELECT 1 FROM encrypted_sessions WHERE site_id = sites.id AND purpose = 'site-http')
		FROM sites WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list all sites: %w", err)
	}
	defer rows.Close()
	return scanSites(rows)
}

func (store *Store) GetSite(ctx context.Context, siteID int64) (Site, error) {
	row := store.db.QueryRowContext(ctx, `
		SELECT id, name, base_url, source_url, adapter_key, adapter_config, custom_failure_reason, enabled, session_required,
			interval_seconds, jitter_seconds, acquisition_state, next_run_at, deleted_at, created_at, updated_at,
			EXISTS (SELECT 1 FROM encrypted_sessions WHERE site_id = sites.id AND purpose = 'site-http')
		FROM sites WHERE id = ? AND deleted_at IS NULL`, siteID)
	var site Site
	var enabled, sessionRequired int
	var intervalSeconds, jitterSeconds int64
	var acquisitionState string
	var nextRunAt sql.NullInt64
	var deletedAt sql.NullInt64
	var createdAt, updatedAt int64
	var sessionConfigured bool
	if err := row.Scan(&site.ID, &site.Name, &site.BaseURL, &site.SourceURL, &site.AdapterKey, &site.AdapterConfig, &site.CustomFailureReason,
		&enabled, &sessionRequired, &intervalSeconds, &jitterSeconds, &acquisitionState, &nextRunAt, &deletedAt, &createdAt, &updatedAt, &sessionConfigured); err != nil {
		return Site{}, fmt.Errorf("get site %d: %w", siteID, err)
	}
	site.Enabled = enabled == 1
	site.SessionRequired = sessionRequired == 1
	site.Interval = time.Duration(intervalSeconds) * time.Second
	site.Jitter = time.Duration(jitterSeconds) * time.Second
	site.IntervalSeconds = intervalSeconds
	site.JitterSeconds = jitterSeconds
	site.AcquisitionState = domain.AcquisitionState(acquisitionState)
	if nextRunAt.Valid {
		t := time.UnixMilli(nextRunAt.Int64).UTC()
		site.NextRunAt = &t
	}
	if deletedAt.Valid {
		value := time.UnixMilli(deletedAt.Int64).UTC()
		site.DeletedAt = &value
	}
	site.SessionConfigured = sessionConfigured
	site.CreatedAt = time.UnixMilli(createdAt).UTC()
	site.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return site, nil
}

func (store *Store) ListDueSites(ctx context.Context, now time.Time, limit int) ([]Site, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT id, name, base_url, source_url, adapter_key, adapter_config, custom_failure_reason, enabled, session_required,
			interval_seconds, jitter_seconds, acquisition_state, next_run_at, deleted_at, created_at, updated_at,
			EXISTS (SELECT 1 FROM encrypted_sessions WHERE site_id = sites.id AND purpose = 'site-http')
		FROM sites WHERE enabled = 1 AND deleted_at IS NULL AND (next_run_at IS NULL OR next_run_at <= ?)
		ORDER BY COALESCE(next_run_at, 0), id LIMIT ?`, unixMilli(now), limit)
	if err != nil {
		return nil, fmt.Errorf("list due sites: %w", err)
	}
	defer rows.Close()
	return scanSites(rows)
}

func (store *Store) SetSiteNextRun(ctx context.Context, siteID int64, nextRunAt time.Time) error {
	if _, err := store.db.ExecContext(ctx, `UPDATE sites SET next_run_at = ? WHERE id = ?`, unixMilli(nextRunAt), siteID); err != nil {
		return fmt.Errorf("set next run: %w", err)
	}
	return nil
}

func (store *Store) UpdateSiteURLs(ctx context.Context, siteID int64, baseURL, sourceURL string) error {
	if siteID <= 0 {
		return errors.New("invalid site ID")
	}
	baseURL, sourceURL = strings.TrimSpace(baseURL), strings.TrimSpace(sourceURL)
	if err := validateSiteURL("base URL", baseURL); err != nil {
		return err
	}
	if err := validateSiteURL("source URL", sourceURL); err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE sites SET base_url = ?, source_url = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, baseURL, sourceURL, unixMilli(time.Now().UTC()), siteID)
	if err != nil {
		return fmt.Errorf("update site URLs: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("site not found")
	}
	return nil
}

func validateSiteURL(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("invalid %s", name)
	}
	return nil
}

func (store *Store) DeleteSite(ctx context.Context, siteID int64, deletedAt time.Time) error {
	if siteID <= 0 {
		return errors.New("invalid site ID")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin site deletion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE sites SET deleted_at = ?, enabled = 0, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, unixMilli(deletedAt), unixMilli(deletedAt), siteID)
	if err != nil {
		return fmt.Errorf("delete site: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("site not found")
	}
	if _, err := incrementRevision(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit site deletion: %w", err)
	}
	return nil
}

func (store *Store) RestoreSite(ctx context.Context, siteID int64, restoredAt time.Time) error {
	if siteID <= 0 {
		return errors.New("invalid site ID")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin site restore: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE sites SET deleted_at = NULL, enabled = 0, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL`, unixMilli(restoredAt), siteID)
	if err != nil {
		return fmt.Errorf("restore site: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return errors.New("deleted site not found")
	}
	if _, err := incrementRevision(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit site restore: %w", err)
	}
	return nil
}

func scanSites(rows siteRows) ([]Site, error) {
	var sites []Site
	for rows.Next() {
		var site Site
		var enabled, sessionRequired int
		var intervalSeconds, jitterSeconds int64
		var acquisitionState string
		var nextRunAt sql.NullInt64
		var deletedAt sql.NullInt64
		var createdAt, updatedAt int64
		var sessionConfigured bool
		if err := rows.Scan(&site.ID, &site.Name, &site.BaseURL, &site.SourceURL, &site.AdapterKey, &site.AdapterConfig, &site.CustomFailureReason,
			&enabled, &sessionRequired, &intervalSeconds, &jitterSeconds, &acquisitionState, &nextRunAt, &deletedAt, &createdAt, &updatedAt, &sessionConfigured); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		site.Enabled = enabled == 1
		site.SessionRequired = sessionRequired == 1
		site.Interval = time.Duration(intervalSeconds) * time.Second
		site.Jitter = time.Duration(jitterSeconds) * time.Second
		site.IntervalSeconds = intervalSeconds
		site.JitterSeconds = jitterSeconds
		site.AcquisitionState = domain.AcquisitionState(acquisitionState)
		if nextRunAt.Valid {
			t := time.UnixMilli(nextRunAt.Int64).UTC()
			site.NextRunAt = &t
		}
		if deletedAt.Valid {
			value := time.UnixMilli(deletedAt.Int64).UTC()
			site.DeletedAt = &value
		}
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
