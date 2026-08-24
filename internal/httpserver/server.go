package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"relayscope/internal/admin"
	"relayscope/internal/collector"
	"relayscope/internal/linuxdo"
	"relayscope/internal/session"
	"relayscope/internal/store"
)

type Options struct {
	Logger       *slog.Logger
	Version      string
	Commit       string
	BuildDate    string
	Now          func() time.Time
	Store        *store.Store
	Auth         *admin.Auth
	Collector    *collector.Collector
	SessionVault *session.Vault
	PublicURL    string
	SessionSync  *session.SyncManager
	LinuxDO      *linuxdo.Service
}

type sessionSyncSite struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Origin     string `json:"origin"`
	LoginURL   string `json:"loginUrl"`
	SourceURL  string `json:"sourceUrl"`
	Reason     string `json:"reason"`
	AdapterKey string `json:"adapterKey"`
}

type sessionSyncBundle struct {
	SiteID int64  `json:"siteId"`
	Origin string `json:"origin"`
	session.Data
}

type publicDashboardCache struct {
	mu          sync.Mutex
	revision    int64
	initialized bool
	payload     []byte
}

func (cache *publicDashboardCache) load(ctx context.Context, dbStore *store.Store, now time.Time) ([]byte, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		revision, err := dbStore.Revision(ctx)
		if err != nil {
			return nil, err
		}
		if cache.initialized && cache.revision == revision {
			return cache.payload, nil
		}
		rows, err := dbStore.QueryPublicRows(ctx, "", "")
		if err != nil {
			return nil, err
		}
		history, err := dbStore.QueryPublicHistory(ctx, now.UTC().Add(-24*time.Hour))
		if err != nil {
			return nil, err
		}
		if latestRevision, revisionErr := dbStore.Revision(ctx); revisionErr != nil {
			return nil, revisionErr
		} else if latestRevision != revision {
			continue
		}
		payload, err := json.Marshal(map[string]any{"revision": revision, "rows": rows, "buckets": history, "hours": 24})
		if err != nil {
			return nil, fmt.Errorf("encode public dashboard: %w", err)
		}
		cache.revision = revision
		cache.initialized = true
		cache.payload = payload
		return cache.payload, nil
	}
	return nil, errors.New("public dashboard changed during read")
}
