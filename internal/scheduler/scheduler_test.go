package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relaypulse/internal/adapter"
	"relaypulse/internal/collector"
	"relaypulse/internal/domain"
	"relaypulse/internal/store"
)

func TestSchedulerStartStop(t *testing.T) {
	t.Parallel()

	dbStore, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()
	registry, err := adapter.NewRegistry(adapter.NewAPIAdapter{})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	siteCollector, err := collector.New(collector.Options{Store: dbStore, Registry: registry, Fetcher: adapter.HTTPFetcher{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("collector: %v", err)
	}
	scheduler := New(dbStore, siteCollector, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	scheduler.Start(context.Background())
	scheduler.Stop()
}

func TestCollectionDelayUsesFailedSiteBackoff(t *testing.T) {
	normal := store.Site{Interval: 15 * time.Minute, AcquisitionState: domain.AcquisitionFresh}
	if got := collectionDelay(normal); got != 15*time.Minute {
		t.Fatalf("normal delay = %s", got)
	}
	for _, state := range []domain.AcquisitionState{domain.AcquisitionCollectionFailed, domain.AcquisitionLoginExpired, domain.AcquisitionChallengePending, domain.AcquisitionChallengeFailed} {
		failed := normal
		failed.AcquisitionState = state
		if got := collectionDelay(failed); got != 30*time.Minute {
			t.Fatalf("%s delay = %s", state, got)
		}
	}
}

func TestListDueSitesRespectsNextRunAt(t *testing.T) {
	ctx := context.Background()
	dbStore, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()

	past, _ := dbStore.CreateSite(ctx, store.Site{
		Name: "past", BaseURL: "https://past.example", SourceURL: "https://past.example/pricing",
		AdapterKey: "test", Enabled: true, Interval: 15 * time.Minute, Jitter: 0,
	})
	future, _ := dbStore.CreateSite(ctx, store.Site{
		Name: "future", BaseURL: "https://future.example", SourceURL: "https://future.example/pricing",
		AdapterKey: "test", Enabled: true, Interval: 15 * time.Minute, Jitter: 0,
	})

	now := time.Now().UTC()
	if err := dbStore.SetSiteNextRun(ctx, past.ID, now.Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := dbStore.SetSiteNextRun(ctx, future.ID, now.Add(1*time.Hour)); err != nil {
		t.Fatal(err)
	}

	due, err := dbStore.ListDueSites(ctx, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != past.ID {
		t.Fatalf("expected only past site to be due, got %d sites", len(due))
	}
}

func TestNewSiteWithNullNextRunAtIsImmediatelyDue(t *testing.T) {
	ctx := context.Background()
	dbStore, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()

	created, err := dbStore.CreateSite(ctx, store.Site{
		Name: "fresh", BaseURL: "https://fresh.example", SourceURL: "https://fresh.example/pricing",
		AdapterKey: "test", Enabled: true, Interval: 15 * time.Minute, Jitter: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	due, err := dbStore.ListDueSites(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != created.ID {
		t.Fatalf("new site with NULL next_run_at should be immediately due, got %d sites", len(due))
	}
}

func TestGetSiteReturnsNextRunAt(t *testing.T) {
	ctx := context.Background()
	dbStore, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()

	created, err := dbStore.CreateSite(ctx, store.Site{
		Name: "scheduled", BaseURL: "https://scheduled.example", SourceURL: "https://scheduled.example/pricing",
		AdapterKey: "test", Enabled: true, Interval: 15 * time.Minute, Jitter: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(1 * time.Hour)
	if err := dbStore.SetSiteNextRun(ctx, created.ID, future); err != nil {
		t.Fatal(err)
	}
	site, err := dbStore.GetSite(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if site.NextRunAt == nil {
		t.Fatal("NextRunAt should not be nil after SetSiteNextRun")
	}
	if !site.NextRunAt.Truncate(time.Second).Equal(future.Truncate(time.Second)) {
		t.Fatalf("NextRunAt = %v, want ~%v", site.NextRunAt, future)
	}
}

func TestScheduleNextPersistsWithIndependentContext(t *testing.T) {
	ctx := context.Background()
	dbStore, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()
	created, err := dbStore.CreateSite(ctx, store.Site{
		Name: "durable", BaseURL: "https://durable.example", SourceURL: "https://durable.example/pricing",
		AdapterKey: "test", Enabled: true, Interval: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := New(dbStore, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	scheduler.scheduleNext(created.ID)
	site, err := dbStore.GetSite(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if site.NextRunAt == nil {
		t.Fatal("scheduleNext did not persist next_run_at")
	}
}

func TestSchedulerDispatchPersistsStateForRestart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":[{"model":"gpt-5.6-sol"}]}`))
	}))
	defer server.Close()
	ctx := context.Background()
	dbStore, err := store.Open(ctx, t.TempDir()+"/restart.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()
	registry, err := adapter.NewRegistry(adapter.NewAPIAdapter{})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	siteCollector, err := collector.New(collector.Options{Store: dbStore, Registry: registry, Fetcher: adapter.HTTPFetcher{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("collector: %v", err)
	}
	site, err := dbStore.CreateSite(ctx, store.Site{
		Name: "restart", BaseURL: server.URL, SourceURL: server.URL + "/api/pricing",
		AdapterKey: "newapi-pricing", Enabled: true, Interval: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := New(dbStore, siteCollector, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	defer scheduler.Stop()
	scheduler.dispatch(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		loaded, loadErr := dbStore.GetSite(ctx, site.ID)
		if loadErr == nil && loaded.NextRunAt != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scheduler dispatch did not persist next_run_at")
}
