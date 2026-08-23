package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

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
