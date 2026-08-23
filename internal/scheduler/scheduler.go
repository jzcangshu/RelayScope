package scheduler

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"relaypulse/internal/collector"
	"relaypulse/internal/domain"
	"relaypulse/internal/store"
)

const scheduledCollectionTimeout = 3 * time.Minute

type Scheduler struct {
	store      *store.Store
	collector  *collector.Collector
	logger     *slog.Logger
	now        func() time.Time
	interval   time.Duration
	stop       chan struct{}
	stopOnce   sync.Once
	loopWait   sync.WaitGroup
	workerWait sync.WaitGroup
	nextMu     sync.Mutex
	nextRun    map[int64]time.Time
}

func New(dbStore *store.Store, siteCollector *collector.Collector, logger *slog.Logger, now func() time.Time) *Scheduler {
	if now == nil {
		now = time.Now
	}
	return &Scheduler{store: dbStore, collector: siteCollector, logger: logger, now: now, interval: 30 * time.Second, stop: make(chan struct{}), nextRun: make(map[int64]time.Time)}
}

func (scheduler *Scheduler) Start(ctx context.Context) {
	scheduler.loopWait.Add(1)
	go func() {
		defer scheduler.loopWait.Done()
		scheduler.dispatch(ctx)
		ticker := time.NewTicker(scheduler.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				scheduler.dispatch(ctx)
			case <-ctx.Done():
				return
			case <-scheduler.stop:
				return
			}
		}
	}()
}

func (scheduler *Scheduler) Stop() {
	scheduler.stopOnce.Do(func() { close(scheduler.stop) })
	scheduler.loopWait.Wait()
	scheduler.workerWait.Wait()
}

func (scheduler *Scheduler) dispatch(ctx context.Context) {
	sites, err := scheduler.store.ListEnabledSites(ctx)
	if err != nil {
		scheduler.logger.Error("list sites for schedule failed", "error", err)
		return
	}
	for _, site := range sites {
		now := scheduler.now().UTC()
		scheduler.nextMu.Lock()
		dueAt, known := scheduler.nextRun[site.ID]
		if known && (dueAt.IsZero() || now.Before(dueAt)) {
			scheduler.nextMu.Unlock()
			continue
		}
		// A zero value marks the site as dispatched until the worker records the
		// next run from the acquisition state produced by this collection.
		scheduler.nextRun[site.ID] = time.Time{}
		scheduler.nextMu.Unlock()
		site := site
		scheduler.workerWait.Add(1)
		go func() {
			defer scheduler.workerWait.Done()
			collectCtx, cancel := context.WithTimeout(ctx, scheduledCollectionTimeout)
			defer cancel()
			if err := scheduler.collector.CollectSite(collectCtx, site, scheduler.now().UTC()); err != nil {
				scheduler.logger.Warn("scheduled collection failed", "site_id", site.ID, "error", err)
			}
			scheduler.scheduleNext(ctx, site)
		}()
	}
}

func (scheduler *Scheduler) scheduleNext(ctx context.Context, previous store.Site) {
	latest := previous
	if sites, err := scheduler.store.ListAllSites(ctx); err == nil {
		for _, site := range sites {
			if site.ID == previous.ID {
				latest = site
				break
			}
		}
	}
	next := scheduler.now().UTC().Add(collectionDelay(latest)).Add(randomJitter(latest.Jitter))
	scheduler.nextMu.Lock()
	scheduler.nextRun[previous.ID] = next
	scheduler.nextMu.Unlock()
}

func collectionDelay(site store.Site) time.Duration {
	switch site.AcquisitionState {
	case domain.AcquisitionCollectionFailed, domain.AcquisitionLoginExpired, domain.AcquisitionChallengePending, domain.AcquisitionChallengeFailed:
		return 30 * time.Minute
	default:
		if site.Interval > 0 {
			return site.Interval
		}
		return 15 * time.Minute
	}
}

func randomJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(max.Milliseconds()+1)) * time.Millisecond
}
