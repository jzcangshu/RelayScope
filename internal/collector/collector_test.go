package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relaypulse/internal/adapter"
	"relaypulse/internal/domain"
	"relaypulse/internal/matcher"
	"relaypulse/internal/store"
)

func TestCollectSiteWritesObservationAndRevision(t *testing.T) {
	t.Parallel()

	dbStore := openCollectorStore(t)
	site, err := dbStore.CreateSite(context.Background(), store.Site{Name: "test", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "newapi-pricing", AdapterConfig: `{}`, Enabled: true, Interval: 20 * time.Minute})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	registry, err := adapter.NewRegistry(adapter.NewAPIAdapter{})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	collector, err := New(Options{Store: dbStore, Registry: registry, Fetcher: fakeJSONFetcher{body: []byte(`{"data":[{"model":"gpt-5.6-sol","group":"free","success_rate":0.99}]}`)}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("collector: %v", err)
	}
	if err := collector.CollectSite(context.Background(), site, time.Now().UTC()); err != nil {
		t.Fatalf("collect site: %v", err)
	}
	revision, err := dbStore.Revision(context.Background())
	if err != nil || revision != 1 {
		t.Fatalf("revision = %d, err=%v", revision, err)
	}
}

type partialDetailAdapter struct{}

func (partialDetailAdapter) Key() string         { return "partial-detail" }
func (partialDetailAdapter) DisplayName() string { return "partial detail" }
func (partialDetailAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (partialDetailAdapter) Collect(_ context.Context, site adapter.Site, _ adapter.Fetcher, now time.Time) (domain.Collection, error) {
	ratio := 1.0
	return domain.Collection{
		SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, CatalogRawNames: []string{"gpt-5.6-sol"},
		Models: []domain.ModelObservation{{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy, Metrics: domain.Metrics{SuccessRatio: &ratio}}}}},
	}, nil
}
func (partialDetailAdapter) CollectDetails(_ context.Context, _ adapter.Site, _ adapter.Fetcher, collection *domain.Collection, _ []string, _ time.Time) error {
	collection.Issues = append(collection.Issues, domain.CollectionIssue{Code: "detail_fetch_failed", Scope: "gpt-5.6-sol", Message: "detail endpoint unavailable"})
	return nil
}

func TestCollectSiteRecordsPartialDetailRun(t *testing.T) {
	dbStore := openCollectorStore(t)
	if err := dbStore.CreateRule(context.Background(), matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	site, err := dbStore.CreateSite(context.Background(), store.Site{Name: "partial", BaseURL: "https://example.test", SourceURL: "https://example.test/status", AdapterKey: "partial-detail", Enabled: true, Interval: 20 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := adapter.NewRegistry(partialDetailAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := New(Options{Store: dbStore, Registry: registry, Fetcher: fakeJSONFetcher{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := collector.CollectSite(context.Background(), site, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	runs, err := dbStore.ListCollectionRuns(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "partial" || runs[0].ErrorCode != "details_partial" || !strings.Contains(runs[0].ErrorMessage, "gpt-5.6-sol") {
		t.Fatalf("partial run = %+v", runs)
	}
}

type failedDetailAdapter struct{ partialDetailAdapter }

func (failedDetailAdapter) Key() string { return "failed-detail" }
func (failedDetailAdapter) CollectDetails(_ context.Context, _ adapter.Site, _ adapter.Fetcher, _ *domain.Collection, _ []string, _ time.Time) error {
	return errors.New("all detail endpoints unavailable")
}

func TestCollectSiteKeepsPreviousSnapshotWhenAllDetailsFail(t *testing.T) {
	dbStore := openCollectorStore(t)
	ctx := context.Background()
	if err := dbStore.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	site, err := dbStore.CreateSite(ctx, store.Site{Name: "failed", BaseURL: "https://example.test", SourceURL: "https://example.test/status", AdapterKey: "failed-detail", Enabled: true, Interval: 20 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 16, 6, 0, 0, 0, time.UTC)
	seed := domain.Collection{
		SiteID: site.ID, ObservedAt: now.Add(-20 * time.Minute), CollectedAt: now.Add(-20 * time.Minute), CatalogComplete: true,
		Models: []domain.ModelObservation{{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceFailed}}}},
	}
	if _, err := dbStore.ApplyCollection(ctx, seed, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	registry, err := adapter.NewRegistry(failedDetailAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := New(Options{Store: dbStore, Registry: registry, Fetcher: fakeJSONFetcher{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := collector.CollectSite(ctx, site, now); err == nil {
		t.Fatal("collection succeeded despite total detail failure")
	}
	var state string
	var collectedAt int64
	if err := dbStore.DB().QueryRowContext(ctx, `SELECT service_state, collected_at FROM current_snapshots LIMIT 1`).Scan(&state, &collectedAt); err != nil {
		t.Fatal(err)
	}
	if state != string(domain.ServiceFailed) || collectedAt != now.Add(-20*time.Minute).UnixMilli() {
		t.Fatalf("previous snapshot changed after failed details: state=%q collected_at=%d", state, collectedAt)
	}
}

type fakeJSONFetcher struct{ body []byte }

func (fetcher fakeJSONFetcher) GetJSON(_ context.Context, _ string, target any) error {
	return json.Unmarshal(fetcher.body, target)
}
func (fetcher fakeJSONFetcher) GetBytes(_ context.Context, _ string) ([]byte, http.Header, error) {
	return fetcher.body, http.Header{}, nil
}

func openCollectorStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relaypulse.db")
	opened, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { opened.Close() })
	return opened
}
