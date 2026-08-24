package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"relaypulse/internal/domain"
)

func (store *Store) ApplyCollection(ctx context.Context, collection domain.Collection, normalize func(string) string) (int64, []int64, error) {
	if err := collection.Validate(); err != nil {
		return 0, nil, fmt.Errorf("validate collection: %w", err)
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
		return 0, nil, fmt.Errorf("begin collection transaction: %w", err)
	}
	defer tx.Rollback()

	affectedRawModels := make([]int64, 0, len(collection.Models))
	for _, model := range collection.Models {
		modelID, err := upsertRawModel(ctx, tx, collection.SiteID, model, collection.CollectedAt, normalize)
		if err != nil {
			return 0, nil, err
		}
		affectedRawModels = append(affectedRawModels, modelID)
		for _, group := range model.Groups {
			groupID, err := upsertGroup(ctx, tx, modelID, group, collection.CollectedAt)
			if err != nil {
				return 0, nil, err
			}
			if err := upsertSnapshot(ctx, tx, groupID, collection.RunID, collection, group); err != nil {
				return 0, nil, err
			}
			for _, bucket := range group.Buckets {
				if err := upsertBucket(ctx, tx, groupID, collection.CollectedAt, bucket); err != nil {
					return 0, nil, err
				}
			}
		}
	}

	if collection.CatalogComplete {
		if collection.MissingCatalogState != "" {
			if err := applyMissingCatalogState(ctx, tx, collection); err != nil {
				return 0, nil, err
			}
		} else if err := updateAbsenceEvidence(ctx, tx, collection.SiteID, collection.CatalogRawNames, collection.CollectedAt); err != nil {
			return 0, nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET acquisition_state = ?, last_success_at = ?, updated_at = ? WHERE id = ?`,
		domain.AcquisitionFresh, unixMilli(collection.CollectedAt), unixMilli(collection.CollectedAt), collection.SiteID); err != nil {
		return 0, nil, fmt.Errorf("update site success: %w", err)
	}

	revision, err := incrementRevision(ctx, tx)
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("commit collection: %w", err)
	}
	return revision, affectedRawModels, nil
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_matches
		WHERE raw_model_id IN (SELECT id FROM raw_models WHERE site_id = ? AND removed_at IS NOT NULL)`, siteID); err != nil {
		return fmt.Errorf("clear matches for removed models: %w", err)
	}
	return nil
}

func applyMissingCatalogState(ctx context.Context, tx *sql.Tx, collection domain.Collection) error {
	// A missing-catalog pass re-admits every raw model of the site before
	// selecting groups, so previously removed models are marked in the same
	// pass instead of resurrecting with their pre-removal snapshot.
	if _, err := tx.ExecContext(ctx, `UPDATE raw_models SET absent_complete_runs = 0, removed_at = NULL WHERE site_id = ?`, collection.SiteID); err != nil {
		return fmt.Errorf("reset presence catalog absence evidence: %w", err)
	}
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
	return nil
}
