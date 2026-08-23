package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"relaypulse/internal/adapter/adapterutil"
	"relaypulse/internal/domain"
	"relaypulse/internal/pricing"
)

type PublicRow struct {
	Provider         string                  `json:"provider"`
	RuleName         string                  `json:"ruleName"`
	SiteID           int64                   `json:"siteId"`
	SiteName         string                  `json:"siteName"`
	SiteURL          string                  `json:"siteUrl"`
	RawModelName     string                  `json:"rawModelName"`
	GroupName        string                  `json:"groupName"`
	ServiceState     domain.ServiceState     `json:"serviceState"`
	AcquisitionState domain.AcquisitionState `json:"acquisitionState"`
	ObservedAt       time.Time               `json:"observedAt"`
	CollectedAt      time.Time               `json:"collectedAt"`
	RequestCount     *int64                  `json:"requestCount,omitempty"`
	SuccessCount     *int64                  `json:"successCount,omitempty"`
	FailureCount     *int64                  `json:"failureCount,omitempty"`
	SuccessRatio     *float64                `json:"successRatio,omitempty"`
	AverageLatencyMS *float64                `json:"averageLatencyMs,omitempty"`
	FirstTokenMS     *float64                `json:"firstTokenMs,omitempty"`
	TokensPerSecond  *float64                `json:"tokensPerSecond,omitempty"`
	Price            *pricing.DisplayPrice   `json:"price,omitempty"`
}

type DetailBucket struct {
	SiteID            int64                 `json:"siteId"`
	SiteName          string                `json:"siteName"`
	SiteURL           string                `json:"siteUrl"`
	RawModelName      string                `json:"rawModelName"`
	GroupName         string                `json:"groupName"`
	ServiceState      domain.ServiceState   `json:"serviceState"`
	Start             time.Time             `json:"start"`
	End               time.Time             `json:"end"`
	ResolutionSeconds int64                 `json:"resolutionSeconds"`
	RequestCount      *int64                `json:"requestCount,omitempty"`
	SuccessCount      *int64                `json:"successCount,omitempty"`
	FailureCount      *int64                `json:"failureCount,omitempty"`
	SuccessRatio      *float64              `json:"successRatio,omitempty"`
	AverageLatencyMS  *float64              `json:"averageLatencyMs,omitempty"`
	FirstTokenMS      *float64              `json:"firstTokenMs,omitempty"`
	TokensPerSecond   *float64              `json:"tokensPerSecond,omitempty"`
	Price             *pricing.DisplayPrice `json:"price,omitempty"`
}

type PublicHistoryBucket struct {
	SiteID       int64               `json:"siteId"`
	RawModelName string              `json:"rawModelName"`
	ServiceState domain.ServiceState `json:"serviceState"`
	Start        time.Time           `json:"start"`
	End          time.Time           `json:"end"`
}

const publicSampleVisibilityWindow = 24 * time.Hour

// Pre-built SQL CASE expressions derived from the shared health thresholds in
// adapterutil, so the Go and SQL thresholds can never diverge.
var ratioStateRankExpr = fmt.Sprintf(`CASE
					WHEN buckets.success_ratio IS NULL THEN 4
					WHEN (CASE WHEN buckets.success_ratio > 1 THEN buckets.success_ratio / 100.0 ELSE buckets.success_ratio END) >= %g THEN 0
					WHEN (CASE WHEN buckets.success_ratio > 1 THEN buckets.success_ratio / 100.0 ELSE buckets.success_ratio END) >= %g THEN 1
					ELSE 2
				END`, adapterutil.HealthyRatio, adapterutil.DegradedRatio)

var ratioStateNameExpr = fmt.Sprintf(`CASE
			WHEN buckets.success_ratio IS NULL THEN 'no_samples'
			WHEN (CASE WHEN buckets.success_ratio > 1 THEN buckets.success_ratio / 100.0 ELSE buckets.success_ratio END) >= %g THEN 'healthy'
			WHEN (CASE WHEN buckets.success_ratio > 1 THEN buckets.success_ratio / 100.0 ELSE buckets.success_ratio END) >= %g THEN 'degraded'
			ELSE 'failed'
		END`, adapterutil.HealthyRatio, adapterutil.DegradedRatio)

var publicVisibleModelPredicate = fmt.Sprintf(` AND (
			site.acquisition_state <> 'fresh'
			OR raw.first_seen_at > strftime('%%s','now') * 1000 - %[1]d
			OR raw.history_coverage_start IS NULL
			OR raw.history_coverage_end IS NULL
			OR raw.history_coverage_end - raw.history_coverage_start < %[1]d
			OR raw.history_coverage_end < strftime('%%s','now') * 1000 - site.interval_seconds * 2000
			OR EXISTS (
				SELECT 1 FROM site_groups evidence_group
				JOIN current_snapshots evidence_snapshot ON evidence_snapshot.group_id = evidence_group.id
				WHERE evidence_group.raw_model_id = raw.id
				  AND (
					COALESCE(evidence_snapshot.request_count, 0) > 0
					OR COALESCE(evidence_snapshot.success_count, 0) > 0
					OR COALESCE(evidence_snapshot.failure_count, 0) > 0
					OR COALESCE(evidence_snapshot.empty_count, 0) > 0
					OR (
						evidence_snapshot.request_count IS NULL
						AND evidence_snapshot.success_count IS NULL
						AND evidence_snapshot.failure_count IS NULL
						AND evidence_snapshot.empty_count IS NULL
						AND (
							evidence_snapshot.success_ratio IS NOT NULL
							OR evidence_snapshot.average_latency_ms IS NOT NULL
							OR evidence_snapshot.first_token_ms IS NOT NULL
							OR evidence_snapshot.tokens_per_second IS NOT NULL
						)
					)
				  )
			)
			OR EXISTS (
				SELECT 1 FROM site_groups bucket_group
				JOIN metric_buckets evidence_bucket ON evidence_bucket.group_id = bucket_group.id
				WHERE bucket_group.raw_model_id = raw.id
				  AND evidence_bucket.bucket_end >= strftime('%%s','now') * 1000 - %[1]d
				  AND (
					COALESCE(evidence_bucket.request_count, 0) > 0
					OR COALESCE(evidence_bucket.success_count, 0) > 0
					OR COALESCE(evidence_bucket.failure_count, 0) > 0
					OR COALESCE(evidence_bucket.empty_count, 0) > 0
					OR (
						evidence_bucket.request_count IS NULL
						AND evidence_bucket.success_count IS NULL
						AND evidence_bucket.failure_count IS NULL
						AND evidence_bucket.empty_count IS NULL
						AND (
							evidence_bucket.success_ratio IS NOT NULL
							OR evidence_bucket.average_latency_ms IS NOT NULL
							OR evidence_bucket.first_token_ms IS NOT NULL
							OR evidence_bucket.tokens_per_second IS NOT NULL
						)
					)
				  )
			)
		)`, publicSampleVisibilityWindow.Milliseconds())

func (store *Store) QueryPublicHistory(ctx context.Context, since time.Time) ([]PublicHistoryBucket, error) {
	// Expand every source interval into the half-hour slots it actually covers.
	// This keeps the dashboard timeline identical to the detail timeline while
	// retaining only the best group state for each model and slot.
	rows, err := store.db.QueryContext(ctx, `WITH RECURSIVE expanded AS (
		SELECT site.id AS site_id, raw.raw_name,
			`+ratioStateRankExpr+` AS state_rank,
			(CAST(buckets.bucket_start / 1800000 AS INTEGER) * 1800000) AS slot_start,
			buckets.bucket_end
		FROM metric_buckets buckets
		JOIN site_groups groups ON groups.id = buckets.group_id
		JOIN raw_models raw ON raw.id = groups.raw_model_id
		JOIN sites site ON site.id = raw.site_id
		JOIN model_matches match ON match.raw_model_id = raw.id AND match.is_primary = 1
		WHERE raw.removed_at IS NULL AND site.enabled = 1 AND buckets.bucket_end >= ?
		UNION ALL
		SELECT site_id, raw_name, state_rank, slot_start + 1800000, bucket_end
		FROM expanded
		WHERE slot_start + 1800000 < bucket_end
	)
	SELECT site_id, raw_name,
		CASE MIN(state_rank)
			WHEN 0 THEN 'healthy'
			WHEN 1 THEN 'degraded'
			WHEN 2 THEN 'failed'
			ELSE 'no_samples'
		END,
		slot_start, slot_start + 1800000
	FROM expanded
	GROUP BY site_id, raw_name, slot_start
	ORDER BY site_id, raw_name, slot_start`, unixMilli(since))
	if err != nil {
		return nil, fmt.Errorf("query public history: %w", err)
	}
	defer rows.Close()

	result := make([]PublicHistoryBucket, 0)
	for rows.Next() {
		var item PublicHistoryBucket
		var state string
		var start, end int64
		if err := rows.Scan(&item.SiteID, &item.RawModelName, &state, &start, &end); err != nil {
			return nil, fmt.Errorf("scan public history: %w", err)
		}
		item.ServiceState = domain.ServiceState(state)
		item.Start = time.UnixMilli(start).UTC()
		item.End = time.UnixMilli(end).UTC()
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public history: %w", err)
	}
	return result, nil
}

func (store *Store) QueryPublicDetails(ctx context.Context, ruleName, siteName, rawModel string, since time.Time) ([]DetailBucket, error) {
	query := `SELECT site.id, site.name, site.source_url, raw.raw_name, groups.raw_name,
		raw.source_extension, groups.source_extension,
	` + ratioStateNameExpr + `, buckets.bucket_start, buckets.bucket_end, buckets.resolution_seconds,
	 buckets.request_count, buckets.success_count, buckets.failure_count, CASE WHEN buckets.success_ratio > 1 THEN buckets.success_ratio / 100.0 ELSE buckets.success_ratio END,
 buckets.average_latency_ms, buckets.first_token_ms, buckets.tokens_per_second
 FROM metric_buckets buckets
 JOIN site_groups groups ON groups.id = buckets.group_id
 JOIN raw_models raw ON raw.id = groups.raw_model_id
 JOIN sites site ON site.id = raw.site_id
 JOIN model_matches match ON match.raw_model_id = raw.id AND match.is_primary = 1
 JOIN model_rules rule ON rule.id = match.rule_id
	WHERE raw.removed_at IS NULL AND site.enabled = 1 AND buckets.bucket_end >= ?`
	args := []any{unixMilli(since)}
	if strings.TrimSpace(ruleName) != "" {
		query += ` AND rule.canonical_name = ?`
		args = append(args, ruleName)
	}
	if strings.TrimSpace(siteName) != "" {
		query += ` AND site.name = ?`
		args = append(args, siteName)
	}
	if strings.TrimSpace(rawModel) != "" {
		query += ` AND raw.raw_name = ?`
		args = append(args, rawModel)
	}
	query += ` ORDER BY raw.raw_name, site.name, groups.raw_name, buckets.bucket_start`
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query public details: %w", err)
	}
	defer rows.Close()
	result := make([]DetailBucket, 0)
	for rows.Next() {
		var item DetailBucket
		var state string
		var start, end int64
		var modelExtension, groupExtension sql.NullString
		if err := rows.Scan(&item.SiteID, &item.SiteName, &item.SiteURL, &item.RawModelName, &item.GroupName, &modelExtension, &groupExtension, &state, &start, &end, &item.ResolutionSeconds, &item.RequestCount, &item.SuccessCount, &item.FailureCount, &item.SuccessRatio, &item.AverageLatencyMS, &item.FirstTokenMS, &item.TokensPerSecond); err != nil {
			return nil, fmt.Errorf("scan public detail: %w", err)
		}
		item.Price = pricing.PriceFromExtensions(nullableBytes(modelExtension), nullableBytes(groupExtension))
		item.ServiceState = domain.ServiceState(state)
		item.Start = time.UnixMilli(start).UTC()
		item.End = time.UnixMilli(end).UTC()
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public details: %w", err)
	}
	return result, nil
}

func (store *Store) QueryPublicRows(ctx context.Context, ruleName, siteName string) ([]PublicRow, error) {
	return store.queryPublicRows(ctx, ruleName, siteName, "", false)
}

func (store *Store) QueryPublicDetailGroups(ctx context.Context, ruleName, siteName, rawModel string) ([]PublicRow, error) {
	return store.queryPublicRows(ctx, ruleName, siteName, rawModel, true)
}

func (store *Store) queryPublicRows(ctx context.Context, ruleName, siteName, rawModel string, exactSite bool) ([]PublicRow, error) {
	query := `
		SELECT COALESCE(rule.provider, raw.provider_hint, ''), COALESCE(rule.canonical_name, ''),
			 site.id, site.name, site.source_url, raw.raw_name, groups.raw_name,
			raw.source_extension, groups.source_extension,
			CASE
				WHEN snapshot.service_state IN ('unknown', 'no_samples') THEN snapshot.service_state
				WHEN snapshot.observed_at < (strftime('%s','now') * 1000 - 7200000) THEN 'no_samples'
				ELSE snapshot.service_state
			END,
			CASE WHEN snapshot.collected_at < (strftime('%s','now') * 1000 - site.interval_seconds * 2000)
				THEN 'stale' ELSE site.acquisition_state END,
			snapshot.observed_at, snapshot.collected_at,
			snapshot.request_count, snapshot.success_count, snapshot.failure_count, CASE WHEN snapshot.success_ratio > 1 THEN snapshot.success_ratio / 100.0 ELSE snapshot.success_ratio END,
			snapshot.average_latency_ms, snapshot.first_token_ms, snapshot.tokens_per_second
		FROM current_snapshots snapshot
		JOIN site_groups groups ON groups.id = snapshot.group_id
		JOIN raw_models raw ON raw.id = groups.raw_model_id
		JOIN sites site ON site.id = raw.site_id
		JOIN model_matches match ON match.raw_model_id = raw.id AND match.is_primary = 1
		JOIN model_rules rule ON rule.id = match.rule_id
		WHERE raw.removed_at IS NULL AND site.enabled = 1` + publicVisibleModelPredicate
	args := make([]any, 0, 2)
	if strings.TrimSpace(ruleName) != "" {
		query += ` AND rule.canonical_name = ?`
		args = append(args, ruleName)
	}
	if strings.TrimSpace(siteName) != "" {
		if exactSite {
			query += ` AND site.name = ?`
			args = append(args, siteName)
		} else {
			query += ` AND site.name LIKE ?`
			args = append(args, "%"+siteName+"%")
		}
	}
	if strings.TrimSpace(rawModel) != "" {
		query += ` AND raw.raw_name = ?`
		args = append(args, rawModel)
	}
	query += ` ORDER BY snapshot.service_state, snapshot.collected_at DESC, site.name, raw.raw_name, groups.raw_name`

	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query public rows: %w", err)
	}
	defer rows.Close()
	result := make([]PublicRow, 0)
	for rows.Next() {
		var row PublicRow
		var provider, ruleCanonical, serviceState, acquisitionState string
		var observedAt, collectedAt int64
		var modelExtension, groupExtension sql.NullString
		if err := rows.Scan(&provider, &ruleCanonical, &row.SiteID, &row.SiteName, &row.SiteURL, &row.RawModelName, &row.GroupName,
			&modelExtension, &groupExtension, &serviceState, &acquisitionState, &observedAt, &collectedAt, &row.RequestCount, &row.SuccessCount, &row.FailureCount,
			&row.SuccessRatio, &row.AverageLatencyMS, &row.FirstTokenMS, &row.TokensPerSecond); err != nil {
			return nil, fmt.Errorf("scan public row: %w", err)
		}
		row.Price = pricing.PriceFromExtensions(nullableBytes(modelExtension), nullableBytes(groupExtension))
		row.Provider, row.RuleName = provider, ruleCanonical
		row.ServiceState = domain.ServiceState(serviceState)
		row.AcquisitionState = domain.AcquisitionState(acquisitionState)
		row.ObservedAt = time.UnixMilli(observedAt).UTC()
		row.CollectedAt = time.UnixMilli(collectedAt).UTC()
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public rows: %w", err)
	}
	return result, nil
}

func nullableBytes(value sql.NullString) []byte {
	if !value.Valid {
		return nil
	}
	return []byte(value.String)
}
