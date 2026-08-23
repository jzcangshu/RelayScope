package bootstrap

import (
	"context"
	"time"

	"relaypulse/internal/store"
)

type SiteSeed struct {
	Name            string
	BaseURL         string
	SourceURL       string
	Adapter         string
	AdapterConfig   string
	SessionRequired bool
}

// InitialSites is intentionally empty. RelayPulse starts with a clean database;
// sites are added through the admin console. See sites.example.json for the
// configuration format.
var InitialSites = []SiteSeed{}

func EnsureInitialSites(ctx context.Context, dbStore *store.Store) error {
	sites, err := dbStore.ListAllSites(ctx)
	if err != nil {
		return err
	}
	existing := make(map[string]struct{}, len(sites))
	for _, site := range sites {
		existing[site.BaseURL] = struct{}{}
	}
	for _, seed := range InitialSites {
		if _, exists := existing[seed.BaseURL]; exists {
			continue
		}
		if _, err := dbStore.CreateSite(ctx, store.Site{
			Name: seed.Name, BaseURL: seed.BaseURL, SourceURL: seed.SourceURL, AdapterKey: seed.Adapter,
			AdapterConfig: defaultAdapterConfig(seed.AdapterConfig), Enabled: true, SessionRequired: seed.SessionRequired, Interval: 15 * time.Minute, Jitter: 2 * time.Minute,
		}); err != nil {
			return err
		}
	}
	return nil
}

func defaultAdapterConfig(value string) string {
	if value == "" {
		return `{}`
	}
	return value
}
