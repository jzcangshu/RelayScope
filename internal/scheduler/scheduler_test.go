package scheduler

import (
	"context"
	"io"
	"log/slog"
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
