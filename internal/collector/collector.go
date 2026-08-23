package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"relaypulse/internal/adapter"
	"relaypulse/internal/domain"
	"relaypulse/internal/matcher"
	"relaypulse/internal/store"
)

type Collector struct {
	store       *store.Store
	registry    *adapter.Registry
	fetcher     adapter.Fetcher
	logger      *slog.Logger
	httpSlots   chan struct{}
	browserLock chan struct{}
	activeMu    sync.Mutex
	activeSites map[int64]struct{}
	matcher     *matcher.Engine
	matcherMu   sync.RWMutex
}

type Options struct {
	Store              *store.Store
	Registry           *adapter.Registry
	Fetcher            adapter.Fetcher
	Logger             *slog.Logger
	MaxHTTPConcurrency int
}

func New(options Options) (*Collector, error) {
	if options.Store == nil || options.Registry == nil || options.Fetcher == nil || options.Logger == nil {
		return nil, fmt.Errorf("store, registry, fetcher, and logger are required")
	}
	if options.MaxHTTPConcurrency <= 0 {
		options.MaxHTTPConcurrency = 3
	}
	rules, err := options.Store.ListRules(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load model rules: %w", err)
	}
	modelMatcher, err := matcher.New(rules)
	if err != nil {
		return nil, fmt.Errorf("build model matcher: %w", err)
	}
	return &Collector{
		store: options.Store, registry: options.Registry, fetcher: options.Fetcher, logger: options.Logger,
		httpSlots: make(chan struct{}, options.MaxHTTPConcurrency), browserLock: make(chan struct{}, 1), activeSites: make(map[int64]struct{}),
		matcher: modelMatcher,
	}, nil
}

func (collector *Collector) CollectSite(ctx context.Context, site store.Site, now time.Time) error {
	if !collector.tryClaimSite(site.ID) {
		return fmt.Errorf("site %d collection is already running", site.ID)
	}
	defer collector.releaseSite(site.ID)

	adapterImpl, ok := collector.registry.Get(site.AdapterKey)
	if !ok {
		return collector.finishFailure(ctx, site, 0, "adapter_not_found", "adapter "+site.AdapterKey+" is not registered", now)
	}
	runID, err := collector.store.StartCollectionRun(ctx, site.ID, site.AdapterKey, now)
	if err != nil {
		return err
	}
	if err := collector.store.SetAcquisitionState(ctx, site.ID, domain.AcquisitionCollecting, now); err != nil {
		collector.logger.Warn("set collecting state failed", "site_id", site.ID, "error", err)
	}

	siteDefinition := adapter.Site{ID: site.ID, Name: site.Name, BaseURL: site.BaseURL, SourceURL: site.SourceURL, ConfigJSON: site.AdapterConfig, SessionRequired: site.SessionRequired}
	fetcher := collector.fetcher
	if siteFetcher, ok := collector.fetcher.(adapter.SiteFetcher); ok {
		resolved, resolveErr := siteFetcher.FetcherForSite(ctx, siteDefinition)
		if resolveErr != nil {
			return collector.finishFailure(ctx, site, runID, "login_expired", resolveErr.Error(), now)
		}
		fetcher = resolved
	}
	collector.httpSlots <- struct{}{}
	collection, collectErr := adapterImpl.Collect(ctx, siteDefinition, fetcher, now)
	<-collector.httpSlots
	if collectErr != nil {
		return collector.finishFailure(ctx, site, runID, classifyFetchError(collectErr), collectErr.Error(), now)
	}
	collection.RunID = runID
	catalogProvided := len(collection.CatalogRawNames) > 0
	if !collection.CatalogComplete || !catalogProvided {
		collection.CatalogRawNames = make([]string, 0, len(collection.Models))
	}
	if collection.CatalogComplete && len(collection.Models) == 0 {
		return collector.finishFailure(ctx, site, runID, "catalog_incomplete", "catalog contained no valid models", now)
	}
	filteredModels := make([]domain.ModelObservation, 0, len(collection.Models))
	matchedNames := make([]string, 0)
	for _, model := range collection.Models {
		if !catalogProvided {
			collection.CatalogRawNames = append(collection.CatalogRawNames, model.RawName)
		}
		collector.matcherMu.RLock()
		matched := len(collector.matcher.Preview(model.RawName).Matches) > 0
		collector.matcherMu.RUnlock()
		if matched {
			filteredModels = append(filteredModels, model)
			matchedNames = append(matchedNames, model.RawName)
		}
	}
	collection.Models = filteredModels
	if detailCollector, ok := adapterImpl.(adapter.DetailCollector); ok {
		collector.httpSlots <- struct{}{}
		detailErr := detailCollector.CollectDetails(ctx, siteDefinition, fetcher, &collection, matchedNames, now)
		<-collector.httpSlots
		if detailErr != nil {
			return collector.finishFailure(ctx, site, runID, classifyFetchError(detailErr), detailErr.Error(), now)
		}
	}
	if err := collection.Validate(); err != nil {
		return collector.finishFailure(ctx, site, runID, "invalid_observation", err.Error(), now)
	}
	revision, err := collector.store.ApplyCollection(ctx, collection, normalizeRawName)
	if err != nil {
		return collector.finishFailure(ctx, site, runID, "store_failed", err.Error(), now)
	}
	collector.matcherMu.RLock()
	matchErr := collector.store.RefreshMatches(ctx, collector.matcher, now)
	collector.matcherMu.RUnlock()
	if matchErr != nil {
		return collector.finishFailure(ctx, site, runID, "match_refresh_failed", matchErr.Error(), now)
	}
	modelCount, groupCount := countObservation(collection)
	status, code, message := "success", "", ""
	if len(collection.Issues) > 0 {
		status = "partial"
		code = "details_partial"
		message = formatCollectionIssues(collection.Issues)
		collector.logger.Warn("site collection completed with partial details", "site_id", site.ID, "issues", len(collection.Issues))
	}
	if err := collector.store.FinishCollectionRun(ctx, runID, status, collection.CatalogComplete, modelCount, groupCount, code, message, now); err != nil {
		return err
	}
	collector.logger.Info("site collection complete", "site_id", site.ID, "models", modelCount, "groups", groupCount, "revision", revision)
	return nil
}

func formatCollectionIssues(issues []domain.CollectionIssue) string {
	const maxIssues = 5
	const maxRunes = 1500
	parts := make([]string, 0, min(len(issues), maxIssues)+1)
	for index, issue := range issues {
		if index >= maxIssues {
			parts = append(parts, fmt.Sprintf("and %d more", len(issues)-maxIssues))
			break
		}
		parts = append(parts, fmt.Sprintf("%s [%s]: %s", issue.Scope, issue.Code, issue.Message))
	}
	message := strings.Join(parts, "; ")
	runes := []rune(message)
	if len(runes) > maxRunes {
		message = string(runes[:maxRunes])
	}
	return message
}

func (collector *Collector) ReloadMatcher(ctx context.Context) error {
	rules, err := collector.store.ListRules(ctx)
	if err != nil {
		return err
	}
	engine, err := matcher.New(rules)
	if err != nil {
		return err
	}
	collector.matcherMu.Lock()
	collector.matcher = engine
	collector.matcherMu.Unlock()
	if err := collector.store.RefreshMatches(ctx, engine, time.Now().UTC()); err != nil {
		return err
	}
	return collector.store.BumpRevision(ctx)
}

func classifyFetchError(err error) string {
	var fetchErr *adapter.FetchError
	if errors.As(err, &fetchErr) {
		if fetchErr.ChallengeFailed {
			return "challenge_failed"
		}
		if fetchErr.LoginRequired || fetchErr.StatusCode == 401 {
			return "login_expired"
		}
		if fetchErr.Challenge {
			return "challenge_pending"
		}
	}
	return "adapter_collect_failed"
}

func (collector *Collector) WithBrowserLease(ctx context.Context, operation func(context.Context) error) error {
	select {
	case collector.browserLock <- struct{}{}:
		defer func() { <-collector.browserLock }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return operation(ctx)
}

func (collector *Collector) Registry() *adapter.Registry { return collector.registry }

func (collector *Collector) CollectNow(ctx context.Context, siteID int64) error {
	sites, err := collector.store.ListAllSites(ctx)
	if err != nil {
		return err
	}
	for _, site := range sites {
		if site.ID == siteID {
			return collector.CollectSite(ctx, site, time.Now().UTC())
		}
	}
	return fmt.Errorf("site %d not found", siteID)
}

func (collector *Collector) finishFailure(ctx context.Context, site store.Site, runID int64, code, message string, now time.Time) error {
	if runID > 0 {
		if err := collector.store.FinishCollectionRun(ctx, runID, "failed", false, 0, 0, code, message, now); err != nil {
			collector.logger.Error("finish failed run failed", "site_id", site.ID, "error", err)
		}
	}
	state := domain.AcquisitionCollectionFailed
	switch code {
	case "login_expired":
		state = domain.AcquisitionLoginExpired
	case "challenge_pending":
		state = domain.AcquisitionChallengePending
	case "challenge_failed":
		state = domain.AcquisitionChallengeFailed
	}
	if err := collector.store.SetAcquisitionState(ctx, site.ID, state, now); err != nil {
		collector.logger.Error("set failed acquisition state failed", "site_id", site.ID, "error", err)
	}
	collector.logger.Warn("site collection failed", "site_id", site.ID, "code", code, "error", message)
	return fmt.Errorf("site %d: %s: %s", site.ID, code, message)
}

func (collector *Collector) tryClaimSite(siteID int64) bool {
	collector.activeMu.Lock()
	defer collector.activeMu.Unlock()
	if _, active := collector.activeSites[siteID]; active {
		return false
	}
	collector.activeSites[siteID] = struct{}{}
	return true
}

func (collector *Collector) releaseSite(siteID int64) {
	collector.activeMu.Lock()
	defer collector.activeMu.Unlock()
	delete(collector.activeSites, siteID)
}

func normalizeRawName(value string) string { return value }

func countObservation(collection domain.Collection) (int, int) {
	groups := 0
	for _, model := range collection.Models {
		groups += len(model.Groups)
	}
	return len(collection.Models), groups
}

func backoff(attempt int) time.Duration {
	if attempt <= 0 {
		return time.Minute
	}
	if attempt > 3 {
		attempt = 3
	}
	return time.Duration(1<<(attempt-1)) * 5 * time.Minute
}

func nextJitter(interval, jitter time.Duration) time.Duration {
	if jitter <= 0 {
		return interval
	}
	return interval + time.Duration(rand.Int64N(jitter.Milliseconds()+1))*time.Millisecond
}

var _ adapter.Fetcher = adapter.HTTPFetcher{}
