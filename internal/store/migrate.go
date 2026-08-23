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
