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
	activeMu   sync.Mutex
	active     map[int64]struct{}
}

func New(dbStore *store.Store, siteCollector *collector.Collector, logger *slog.Logger, now func() time.Time) *Scheduler {
	if now == nil {
		now = time.Now
	}
	return &Scheduler{store: dbStore, collector: siteCollector, logger: logger, now: now, interval: 30 * time.Second, stop: make(chan struct{}), active: make(map[int64]struct{})}
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
	now := scheduler.now().UTC()
	sites, err := scheduler.store.ListDueSites(ctx, now, 100)
	if err != nil {
		scheduler.logger.Error("list due sites failed", "error", err)
		return
	}
	for _, site := range sites {
		if !scheduler.tryClaim(site.ID) {
			continue
		}
		site := site
		scheduler.workerWait.Add(1)
		go func() {
			defer scheduler.workerWait.Done()
			defer scheduler.release(site.ID)
			collectCtx, cancel := context.WithTimeout(ctx, scheduledCollectionTimeout)
			defer cancel()
			if err := scheduler.collector.CollectSite(collectCtx, site, scheduler.now().UTC()); err != nil {
				scheduler.logger.Warn("scheduled collection failed", "site_id", site.ID, "error", err)
			}
			scheduler.scheduleNext(ctx, site.ID)
		}()
	}
}

func (scheduler *Scheduler) scheduleNext(ctx context.Context, siteID int64) {
	latest, err := scheduler.store.GetSite(ctx, siteID)
	if err != nil {
		scheduler.logger.Warn("get site for scheduling failed", "site_id", siteID, "error", err)
		return
	}
	next := scheduler.now().UTC().Add(collectionDelay(latest)).Add(randomJitter(latest.Jitter))
	if err := scheduler.store.SetSiteNextRun(ctx, siteID, next); err != nil {
		scheduler.logger.Warn("set next run failed", "site_id", siteID, "error", err)
	}
}

func (scheduler *Scheduler) tryClaim(siteID int64) bool {
	scheduler.activeMu.Lock()
	defer scheduler.activeMu.Unlock()
	if _, exists := scheduler.active[siteID]; exists {
		return false
	}
	scheduler.active[siteID] = struct{}{}
	return true
}

func (scheduler *Scheduler) release(siteID int64) {
	scheduler.activeMu.Lock()
	defer scheduler.activeMu.Unlock()
	delete(scheduler.active, siteID)
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
