package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"relayscope/internal/adapter/adapterutil"
	"relayscope/internal/domain"
	"relayscope/internal/pricing"
)

// ModelStatusAdapter reads the public model-status report exposed by enhanced
// NewAPI deployments (for example ai.venlacy.com). The report is the source of
// health and 30-minute history; the companion NewAPI pricing endpoint supplies
// model and group quotes. No session is required.
type ModelStatusAdapter struct {
	PricingRegistry *pricing.Registry
}

func (ModelStatusAdapter) Key() string         { return "newapi-model-status" }
func (ModelStatusAdapter) DisplayName() string { return "NewAPI 模型状态" }
func (ModelStatusAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"modelStatusPath":{"type":"string","default":"/api/enhancements/model-status/embed/status/all"},"pricingAdapter":{"type":"string","default":"newapi"},"pricingPath":{"type":"string","default":"/api/pricing"},"pricingStatusPath":{"type":"string","default":"/api/status"},"announcementMode":{"type":"string","enum":["auto","timeline","notice_diff","disabled"],"default":"auto"}}}`)
}

type modelStatusConfig struct {
	ModelStatusPath   string `json:"modelStatusPath"`
	PricingAdapter    string `json:"pricingAdapter"`
	PricingPath       string `json:"pricingPath"`
	PricingStatusPath string `json:"pricingStatusPath"`
	AnnouncementMode  string `json:"announcementMode"`
}

type modelStatusResponse struct {
	Success bool               `json:"success"`
	Message string             `json:"message"`
	Data    []modelStatusEntry `json:"data"`
}

type modelStatusEntry struct {
	ModelName     string            `json:"model_name"`
	Group         string            `json:"group"`
	CurrentStatus string            `json:"current_status"`
	Status        string            `json:"status"`
	TotalRequests int64             `json:"total_requests"`
	SuccessCount  int64             `json:"success_count"`
	ErrorCount    int64             `json:"error_count"`
	SuccessRate   *float64          `json:"success_rate"`
	AverageTimeS  *float64          `json:"avg_use_time"`
	FirstResponse *float64          `json:"recent_avg_first_response_time"`
	TokenSpeed    *float64          `json:"recent_avg_output_token_speed"`
	LastRequestAt int64             `json:"last_request_at"`
	SlotData      []modelStatusSlot `json:"slot_data"`
}

type modelStatusSlot struct {
	StartTime     int64    `json:"start_time"`
	EndTime       int64    `json:"end_time"`
	TotalRequests int64    `json:"total_requests"`
	SuccessCount  int64    `json:"success_count"`
	ErrorCount    int64    `json:"error_count"`
	SuccessRate   *float64 `json:"success_rate"`
}

func (adapter ModelStatusAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return domain.Collection{}, fmt.Errorf("apply %s config defaults: %w", adapter.Key(), err)
	}
	var config modelStatusConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return domain.Collection{}, fmt.Errorf("decode %s config: %w", adapter.Key(), err)
	}
	endpoint, err := resolveSiteURL(site.BaseURL, config.ModelStatusPath)
	if err != nil {
		return domain.Collection{}, err
	}
	body, _, err := fetcher.GetBytes(ctx, endpoint)
	if err != nil {
		return domain.Collection{}, err
	}
	var response modelStatusResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return domain.Collection{}, fmt.Errorf("decode model-status response: %w", err)
	}
	if !response.Success {
		message := strings.TrimSpace(response.Message)
		if message == "" {
			message = "model-status response was unsuccessful"
		}
		return domain.Collection{}, fmt.Errorf("%s", message)
	}

	collection := domain.Collection{SiteID: site.ID, ObservedAt: now.UTC(), CollectedAt: now.UTC(), CatalogComplete: true}
	if len(response.Data) == 0 {
		return markEmptyProbeCatalog(collection), nil
	}
	type modelStatusModel struct {
		name     string
		groups   []domain.GroupObservation
		grouped  map[string]bool
		coverage modelStatusCoverage
	}
	ordered := make([]*modelStatusModel, 0, len(response.Data))
	byName := make(map[string]*modelStatusModel, len(response.Data))
	for _, entry := range response.Data {
		modelName := strings.TrimSpace(entry.ModelName)
		if modelName == "" {
			continue
		}
		model := byName[modelName]
		if model == nil {
			model = &modelStatusModel{name: modelName, grouped: make(map[string]bool)}
			byName[modelName] = model
			ordered = append(ordered, model)
		}
		groupName := strings.TrimSpace(entry.Group)
		if groupName == "" {
			groupName = "default"
		}
		if model.grouped[groupName] {
			continue
		}
		model.grouped[groupName] = true
		group := modelStatusGroupObservation(entry, groupName)
		model.coverage.expand(group.Buckets)
		model.groups = append(model.groups, group)
	}
	if len(ordered) == 0 {
		return markEmptyProbeCatalog(collection), nil
	}
	for _, model := range ordered {
		observation := domain.ModelObservation{RawName: model.name, Groups: model.groups}
		if model.coverage.complete(now.UTC()) {
			observation.HistoryCoverageStart = model.coverage.earliest
			observation.HistoryCoverageEnd = now.UTC()
		}
		collection.Models = append(collection.Models, observation)
	}
	collection.CatalogRawNames = make([]string, 0, len(collection.Models))
	for _, model := range collection.Models {
		collection.CatalogRawNames = append(collection.CatalogRawNames, model.RawName)
	}
	if config.PricingAdapter != "" && config.PricingPath != "" {
		if err := attachPricingSource(ctx, site, fetcher, adapter.PricingRegistry, pricingSource{DecoderKey: config.PricingAdapter, Path: config.PricingPath, StatusPath: config.PricingStatusPath}, &collection); err != nil {
			return domain.Collection{}, fmt.Errorf("attach model-status pricing: %w", err)
		}
	}
	return collection, nil
}

// modelStatusGroupObservation converts one report row into a group observation.
// The 24h window aggregates feed the current metrics; the slot rows feed the
// bounded 30-minute history buckets.
func modelStatusGroupObservation(entry modelStatusEntry, groupName string) domain.GroupObservation {
	group := domain.GroupObservation{
		RawName:      groupName,
		ServiceState: modelStatusState(entry.CurrentStatus, entry.Status, entry.SuccessRate),
		Metrics:      modelStatusMetrics(entry),
	}
	if entry.LastRequestAt > 0 {
		group.ObservedAt = time.Unix(entry.LastRequestAt, 0).UTC()
	}
	var latest time.Time
	for _, slot := range entry.SlotData {
		if slot.StartTime <= 0 || slot.EndTime < slot.StartTime {
			continue
		}
		start := time.Unix(slot.StartTime, 0).UTC()
		end := time.Unix(slot.EndTime, 0).UTC()
		group.Buckets = append(group.Buckets, domain.TimeBucket{
			Start:      start,
			End:        end,
			Resolution: end.Sub(start),
			Metrics:    modelStatusSlotMetrics(slot),
		})
		if latest.IsZero() || end.After(latest) {
			latest = end
		}
	}
	if group.ObservedAt.IsZero() {
		group.ObservedAt = latest
	}
	return group
}

func modelStatusMetrics(entry modelStatusEntry) domain.Metrics {
	metrics := domain.Metrics{
		SuccessRatio: adapterutil.NormalizeRatio(entry.SuccessRate),
	}
	if entry.TotalRequests > 0 {
		failures := entry.ErrorCount
		if failures < 0 {
			failures = 0
		}
		if failures > entry.TotalRequests {
			failures = entry.TotalRequests
		}
		metrics.RequestCount = int64Pointer(entry.TotalRequests)
		metrics.FailureCount = int64Pointer(failures)
		metrics.SuccessCount = int64Pointer(entry.TotalRequests - failures)
	}
	if entry.AverageTimeS != nil && *entry.AverageTimeS > 0 {
		latency := *entry.AverageTimeS * 1000
		metrics.AverageLatencyMS = &latency
	}
	if entry.FirstResponse != nil && *entry.FirstResponse > 0 {
		firstToken := *entry.FirstResponse * 1000
		metrics.FirstTokenMS = &firstToken
	}
	if entry.TokenSpeed != nil && *entry.TokenSpeed > 0 {
		speed := *entry.TokenSpeed
		metrics.TokensPerSecond = &speed
	}
	return metrics
}

func modelStatusSlotMetrics(slot modelStatusSlot) domain.Metrics {
	metrics := domain.Metrics{
		SuccessRatio: adapterutil.NormalizeRatio(slot.SuccessRate),
	}
	if slot.TotalRequests > 0 {
		failures := slot.ErrorCount
		if failures < 0 {
			failures = 0
		}
		if failures > slot.TotalRequests {
			failures = slot.TotalRequests
		}
		metrics.RequestCount = int64Pointer(slot.TotalRequests)
		metrics.FailureCount = int64Pointer(failures)
		metrics.SuccessCount = int64Pointer(slot.TotalRequests - failures)
	}
	return metrics
}

// modelStatusState prefers the report's traffic-light status and falls back to
// the wordly status field, then to the success ratio.
func modelStatusState(currentStatus, status string, successRate *float64) domain.ServiceState {
	switch strings.ToLower(strings.TrimSpace(currentStatus)) {
	case "green":
		return domain.ServiceHealthy
	case "yellow":
		return domain.ServiceDegraded
	case "red":
		return domain.ServiceFailed
	}
	return modelProbeState(status, successRate)
}

// modelStatusCoverage tracks the slot window of one model so the collection
// can declare authoritative 24h history coverage when the report is complete
// and current.
type modelStatusCoverage struct {
	earliest time.Time
	latest   time.Time
}

func (coverage *modelStatusCoverage) expand(buckets []domain.TimeBucket) {
	for _, bucket := range buckets {
		if coverage.earliest.IsZero() || bucket.Start.Before(coverage.earliest) {
			coverage.earliest = bucket.Start
		}
		if coverage.latest.IsZero() || bucket.End.After(coverage.latest) {
			coverage.latest = bucket.End
		}
	}
}

// complete mirrors the model-probe freshness rule: the reported window must
// reach back at least 23 hours and its newest bucket must be at most one hour
// old, so the board can trust the coverage as a continuous 24h window.
func (coverage modelStatusCoverage) complete(now time.Time) bool {
	if coverage.earliest.IsZero() || coverage.latest.IsZero() {
		return false
	}
	if coverage.earliest.After(now.Add(-23 * time.Hour)) {
		return false
	}
	return !coverage.latest.Before(now.Add(-time.Hour))
}

// CollectAnnouncements implements AnnouncementProvider. These sites are NewAPI
// deployments, so announcements come from /api/status.
func (adapter ModelStatusAdapter) CollectAnnouncements(ctx context.Context, site Site, fetcher Fetcher) ([]Announcement, error) {
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return nil, fmt.Errorf("apply %s config defaults: %w", adapter.Key(), err)
	}
	var config modelStatusConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return nil, fmt.Errorf("decode %s announcement config: %w", adapter.Key(), err)
	}
	statusPath := strings.TrimSpace(config.PricingStatusPath)
	if statusPath == "" {
		statusPath = "/api/status"
	}
	return collectNewAPIAnnouncementsFor(ctx, fetcher, site.BaseURL, sourceOriginBaseURL(site.SourceURL), statusPath, config.AnnouncementMode)
}
