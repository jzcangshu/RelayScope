package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"relaypulse/internal/adapter/adapterutil"
	"relaypulse/internal/domain"
	"relaypulse/internal/pricing"
)

// ModelPulseAdapter collects public, minute-granularity model activity feeds.
// Pricing is fetched separately because these feeds contain health only.
type ModelPulseAdapter struct {
	PricingRegistry *pricing.Registry
}

func (ModelPulseAdapter) Key() string         { return "model-pulse" }
func (ModelPulseAdapter) DisplayName() string { return "模型活动状态" }
func (ModelPulseAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pulsePath":{"type":"string","default":"/api/model-pulse"},"pricingAdapter":{"type":"string","default":"newapi"},"pricingPath":{"type":"string","default":"/api/pricing"},"pricingStatusPath":{"type":"string","default":"/api/status"}}}`)
}

type modelPulseConfig struct {
	PulsePath         string `json:"pulsePath"`
	PricingAdapter    string `json:"pricingAdapter"`
	PricingPath       string `json:"pricingPath"`
	PricingStatusPath string `json:"pricingStatusPath"`
}

type modelPulseResponse struct {
	Success *bool `json:"success"`
	Data    struct {
		Models []modelPulseModel `json:"models"`
	} `json:"data"`
}

type modelPulseModel struct {
	Model        string             `json:"model"`
	Requests     int64              `json:"requests"`
	Failures     int64              `json:"failures"`
	SuccessRate  *float64           `json:"success_rate"`
	AverageMS    *float64           `json:"avg_latency_ms"`
	LastActiveAt int64              `json:"last_active_at"`
	Minutes      []modelPulseMinute `json:"minutes"`
}

type modelPulseMinute struct {
	Timestamp int64 `json:"ts"`
	Requests  int64 `json:"requests"`
	Failures  int64 `json:"failures"`
}

func (adapter ModelPulseAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	config := modelPulseConfig{PulsePath: "/api/model-pulse", PricingAdapter: "newapi", PricingPath: "/api/pricing", PricingStatusPath: "/api/status"}
	if strings.TrimSpace(site.ConfigJSON) != "" {
		if err := json.Unmarshal([]byte(site.ConfigJSON), &config); err != nil {
			return domain.Collection{}, fmt.Errorf("decode model-pulse config: %w", err)
		}
	}
	if strings.TrimSpace(config.PulsePath) == "" {
		config.PulsePath = "/api/model-pulse"
	}
	endpoint, err := resolveSiteURL(site.BaseURL, config.PulsePath)
	if err != nil {
		return domain.Collection{}, err
	}
	var payload modelPulseResponse
	if err := fetcher.GetJSON(ctx, endpoint, &payload); err != nil {
		return domain.Collection{}, err
	}
	if payload.Success != nil && !*payload.Success {
		return domain.Collection{}, fmt.Errorf("model-pulse response was unsuccessful")
	}

	collection := domain.Collection{SiteID: site.ID, ObservedAt: now.UTC(), CollectedAt: now.UTC(), CatalogComplete: true}
	for _, source := range payload.Data.Models {
		name := strings.TrimSpace(source.Model)
		if name == "" {
			continue
		}
		metrics := modelPulseMetrics(source.Requests, source.Failures, source.SuccessRate, source.AverageMS)
		group := domain.GroupObservation{RawName: "default", ServiceState: serviceState("", metrics.SuccessRatio), Metrics: metrics}
		if source.LastActiveAt > 0 {
			group.ObservedAt = modelPulseTime(source.LastActiveAt)
		}
		for _, minute := range source.Minutes {
			start := modelPulseTime(minute.Timestamp)
			if start.IsZero() || start.After(now.UTC()) {
				continue
			}
			bucketMetrics := modelPulseMetrics(minute.Requests, minute.Failures, nil, nil)
			group.Buckets = append(group.Buckets, domain.TimeBucket{Start: start, End: start.Add(time.Minute), Resolution: time.Minute, Metrics: bucketMetrics})
			if group.ObservedAt.IsZero() && minute.Requests > 0 {
				group.ObservedAt = start
			}
		}
		if group.ObservedAt.IsZero() && len(group.Buckets) > 0 {
			group.ObservedAt = group.Buckets[len(group.Buckets)-1].Start
		}
		collection.Models = append(collection.Models, domain.ModelObservation{RawName: name, Groups: []domain.GroupObservation{group}})
		collection.CatalogRawNames = append(collection.CatalogRawNames, name)
	}
	if len(collection.Models) == 0 {
		return domain.Collection{}, fmt.Errorf("model-pulse response contained no valid models")
	}
	if config.PricingAdapter != "" && config.PricingPath != "" {
		if err := attachPricingSource(ctx, site, fetcher, adapter.PricingRegistry, pricingSource{DecoderKey: config.PricingAdapter, Path: config.PricingPath, StatusPath: config.PricingStatusPath}, &collection); err != nil {
			return domain.Collection{}, fmt.Errorf("attach model-pulse pricing: %w", err)
		}
	}
	return collection, nil
}

func modelPulseMetrics(requests, failures int64, reportedRatio, averageMS *float64) domain.Metrics {
	metrics := domain.Metrics{AverageLatencyMS: averageMS}
	if requests <= 0 {
		return metrics
	}
	if failures < 0 {
		failures = 0
	}
	if failures > requests {
		failures = requests
	}
	successes := requests - failures
	metrics.RequestCount = int64Pointer(requests)
	metrics.SuccessCount = int64Pointer(successes)
	metrics.FailureCount = int64Pointer(failures)
	if reportedRatio != nil {
		metrics.SuccessRatio = adapterutil.NormalizeRatio(reportedRatio)
	} else {
		ratio := float64(successes) / float64(requests)
		metrics.SuccessRatio = &ratio
	}
	return metrics
}

func modelPulseTime(value int64) time.Time {
	return adapterutil.ParseFlexibleTime(value)
}
