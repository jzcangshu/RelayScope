package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"relayscope/internal/store"
)

func TestInitialSitesIsEmptyForCleanStart(t *testing.T) {
	if len(InitialSites) != 0 {
		t.Fatalf("InitialSites should be empty for a clean open-source start, got %d entries", len(InitialSites))
	}
}

func TestEnsureInitialSitesIsNoOpOnEmptyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relayscope.db")
	dbStore, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()

	if err := EnsureInitialSites(context.Background(), dbStore); err != nil {
		t.Fatalf("EnsureInitialSites on empty seed: %v", err)
	}
	sites, err := dbStore.ListAllSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 0 {
		t.Fatalf("expected empty database, got %d sites", len(sites))
	}
}

func TestDefaultAdapterConfig(t *testing.T) {
	if got := defaultAdapterConfig(""); got != `{}` {
		t.Fatalf("empty adapter config = %q, want {}", got)
	}
	if got := defaultAdapterConfig(`{"skipDetails":true}`); got != `{"skipDetails":true}` {
		t.Fatalf("configured adapter config changed: %q", got)
	}
}
