package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"relayscope/internal/domain"
)

func (store *Store) ListCollectionRuns(ctx context.Context, limit int) ([]CollectionRun, error) {
	return store.ListCollectionRunsFiltered(ctx, RunFilters{Limit: limit})
}

func (store *Store) ListCollectionRunsFiltered(ctx context.Context, filters RunFilters) ([]CollectionRun, error) {
	if filters.Limit <= 0 || filters.Limit > 200 {
		filters.Limit = 50
	}
	query := `
		SELECT run.id, run.site_id, site.name, run.adapter_key, run.started_at, run.finished_at, run.status,
			run.catalog_complete, run.models_seen, run.groups_seen, COALESCE(run.error_code, ''), COALESCE(run.error_message, '')
		FROM collection_runs run JOIN sites site ON site.id = run.site_id
		WHERE site.deleted_at IS NULL`
	args := make([]any, 0, 4)
	if filters.SiteID > 0 {
		query += ` AND run.site_id = ?`
		args = append(args, filters.SiteID)
	}
	if strings.TrimSpace(filters.Status) != "" {
		query += ` AND run.status = ?`
		args = append(args, filters.Status)
	}
	if filters.Since != nil {
		query += ` AND run.started_at >= ?`
		args = append(args, unixMilli(*filters.Since))
	}
	query += ` ORDER BY run.started_at DESC, run.id DESC LIMIT ?`
	args = append(args, filters.Limit)
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list collection runs: %w", err)
	}
	defer rows.Close()
	runs := make([]CollectionRun, 0, filters.Limit)
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
			WHERE site.deleted_at IS NULL
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

// RecoverRunningCollectionRuns closes runs left behind by a process restart.
// The scheduler is started only after this reconciliation, so every running
// row at startup belongs to work that can no longer complete in this process.
func (store *Store) RecoverRunningCollectionRuns(ctx context.Context, finishedAt time.Time) (int64, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin running run recovery: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE collection_runs
		SET finished_at = ?, status = 'failed', error_code = 'process_restarted',
			error_message = 'collection interrupted by process restart'
		WHERE status = 'running'`, unixMilli(finishedAt))
	if err != nil {
		return 0, fmt.Errorf("recover running collection runs: %w", err)
	}
	runCount, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count recovered collection runs: %w", err)
	}
	stateResult, err := tx.ExecContext(ctx, `
		UPDATE sites SET acquisition_state = 'collection_failed', updated_at = ?
		WHERE acquisition_state = 'collecting'`, unixMilli(finishedAt))
	if err != nil {
		return 0, fmt.Errorf("recover collecting site states: %w", err)
	}
	stateCount, err := stateResult.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count recovered site states: %w", err)
	}
	if runCount > 0 || stateCount > 0 {
		if _, err := incrementRevision(ctx, tx); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit running run recovery: %w", err)
	}
	return runCount, nil
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
