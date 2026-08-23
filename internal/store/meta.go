package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

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
