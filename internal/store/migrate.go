package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

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
	if err := store.ensureSiteSchema(ctx); err != nil {
		return err
	}
	return nil
}

// ensureSiteSchema reconciles the columns used by the current query layer
// with databases created by the predecessor project. That project used a
// separate migration sequence and can report a higher schema_version while
// still lacking columns introduced here.
func (store *Store) ensureSiteSchema(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(sites)`)
	if err != nil {
		return fmt.Errorf("inspect sites schema: %w", err)
	}
	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("read sites schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate sites schema: %w", err)
	}
	rows.Close()
	if len(columns) == 0 {
		return fmt.Errorf("inspect sites schema: sites table is missing")
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sites schema reconciliation: %w", err)
	}
	defer tx.Rollback()
	if _, ok := columns["next_run_at"]; !ok {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE sites ADD COLUMN next_run_at INTEGER`); err != nil {
			return fmt.Errorf("add sites.next_run_at: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sites SET next_run_at = CAST(strftime('%s', 'now') AS INTEGER) * 1000 + ((id * 137) % 900000) WHERE enabled = 1 AND next_run_at IS NULL`); err != nil {
			return fmt.Errorf("seed sites.next_run_at: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS sites_schedule_idx ON sites(enabled, next_run_at)`); err != nil {
		return fmt.Errorf("create sites schedule index: %w", err)
	}
	if _, ok := columns["deleted_at"]; !ok {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE sites ADD COLUMN deleted_at INTEGER`); err != nil {
			return fmt.Errorf("add sites.deleted_at: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS sites_active_idx ON sites(enabled, deleted_at, id)`); err != nil {
		return fmt.Errorf("create sites active index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sites schema reconciliation: %w", err)
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
