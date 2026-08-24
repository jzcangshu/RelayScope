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

// ModelProbeAdapter reads the authenticated model-probe report exposed by
// NewAPI deployments such as Jianzhile. The probe response is the source of
// health and hourly history; the companion NewAPI pricing endpoint supplies
// model and group quotes.
type ModelProbeAdapter struct {
	PricingRegistry *pricing.Registry
}

func (ModelProbeAdapter) Key() string         { return "model-probe" }
func (ModelProbeAdapter) DisplayName() string { return "NewAPI 模型探测" }
func (ModelProbeAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"modelProbePath":{"type":"string","default":"/api/model_probe/status"},"pricingAdapter":{"type":"string","default":"newapi"},"pricingPath":{"type":"string","default":"/api/pricing"},"pricingStatusPath":{"type":"string","default":"/api/status"}}}`)
}

type modelProbeConfig struct {
	ModelProbePath    string `json:"modelProbePath"`
	PricingAdapter    string `json:"pricingAdapter"`
	PricingPath       string `json:"pricingPath"`
	PricingStatusPath string `json:"pricingStatusPath"`
}

type modelProbeResponse struct {
	Success bool             `json:"success"`
	Message string           `json:"message"`
	Data    modelProbeReport `json:"data"`
}

type modelProbeReport struct {
	Models []modelProbeModel `json:"models"`
}

type modelProbeModel struct {
	ModelName   string             `json:"model_name"`
	Status      string             `json:"status"`
	Window24H   modelProbeWindow   `json:"window_24h"`
	Hourly      []modelProbeBucket `json:"hourly"`
	LastProbeAt int64              `json:"last_probe_at"`
	AverageMS   *float64           `json:"avg_latency_ms"`
}

type modelProbeWindow struct {
	Requests    int64    `json:"requests"`
	Success     int64    `json:"success"`
	SuccessRate *float64 `json:"success_rate"`
}

type modelProbeBucket struct {
	HourStart   int64    `json:"hour_start"`
	Requests    int64    `json:"requests"`
	Success     int64    `json:"success"`
	SuccessRate *float64 `json:"success_rate"`
}

func (adapter ModelProbeAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return domain.Collection{}, fmt.Errorf("apply model-probe config defaults: %w", err)
	}
	var config modelProbeConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return domain.Collection{}, fmt.Errorf("decode model-probe config: %w", err)
	}
	endpoint, err := resolveSiteURL(site.BaseURL, config.ModelProbePath)
	if err != nil {
		return domain.Collection{}, err
	}
	body, _, err := fetcher.GetBytes(ctx, endpoint)
	if err != nil {
		return domain.Collection{}, err
	}
	var response modelProbeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return domain.Collection{}, fmt.Errorf("decode model-probe response: %w", err)
	}
	if !response.Success {
		message := strings.TrimSpace(response.Message)
		if message == "" {
			message = "model-probe response was unsuccessful"
		}
		return domain.Collection{}, fmt.Errorf("%s", message)
	}

	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true}
	if len(response.Data.Models) == 0 {
		return markEmptyProbeCatalog(collection), nil
	}
	for _, source := range response.Data.Models {
		name := strings.TrimSpace(source.ModelName)
		if name == "" {
			continue
		}
		group := domain.GroupObservation{RawName: "default", ServiceState: modelProbeState(source.Status, source.Window24H.SuccessRate), Metrics: modelProbeWindowMetrics(source.Window24H, source.AverageMS)}
		if source.LastProbeAt > 0 {
			group.ObservedAt = time.Unix(source.LastProbeAt, 0).UTC()
		}
		var earliest, latest time.Time
		for _, bucket := range source.Hourly {
			if bucket.HourStart <= 0 {
				continue
			}
			start := time.Unix(bucket.HourStart, 0).UTC()
			if start.After(now.UTC()) || start.Before(now.UTC().Add(-72*time.Hour)) {
				continue
			}
			metrics := modelProbeBucketMetrics(bucket)
			group.Buckets = append(group.Buckets, domain.TimeBucket{Start: start, End: start.Add(time.Hour), Resolution: time.Hour, Metrics: metrics})
			if earliest.IsZero() || start.Before(earliest) {
				earliest = start
			}
			if latest.IsZero() || start.After(latest) {
				latest = start
			}
		}
		if group.ObservedAt.IsZero() {
			group.ObservedAt = latest
		}
		observation := domain.ModelObservation{RawName: name, Groups: []domain.GroupObservation{group}}
		if !earliest.IsZero() && !latest.IsZero() && !earliest.After(now.UTC().Add(-23*time.Hour)) && !latest.Before(now.UTC().Add(-time.Hour)) {
			observation.HistoryCoverageStart = earliest
			observation.HistoryCoverageEnd = now.UTC()
		}
		collection.Models = append(collection.Models, observation)
	}
	if len(collection.Models) == 0 {
		return markEmptyProbeCatalog(collection), nil
	}
	collection.CatalogRawNames = make([]string, 0, len(collection.Models))
	for _, model := range collection.Models {
		collection.CatalogRawNames = append(collection.CatalogRawNames, model.RawName)
	}
	if config.PricingAdapter != "" && config.PricingPath != "" {
		if err := attachPricingSource(ctx, site, fetcher, adapter.PricingRegistry, pricingSource{DecoderKey: config.PricingAdapter, Path: config.PricingPath, StatusPath: config.PricingStatusPath}, &collection); err != nil {
			return domain.Collection{}, fmt.Errorf("attach model-probe pricing: %w", err)
		}
	}
	return collection, nil
}

// markEmptyProbeCatalog turns a successful but empty probe report into an
// empty catalog whose absent models are all marked failed by the store.
func markEmptyProbeCatalog(collection domain.Collection) domain.Collection {
	collection.MissingCatalogState = domain.ServiceFailed
	return collection
}

func modelProbeWindowMetrics(window modelProbeWindow, averageMS *float64) domain.Metrics {
	metrics := domain.Metrics{}
	if window.Requests > 0 {
		metrics.RequestCount = int64Pointer(window.Requests)
		metrics.SuccessCount = int64Pointer(window.Success)
		failure := window.Requests - window.Success
		if failure < 0 {
			failure = 0
		}
		metrics.FailureCount = int64Pointer(failure)
	}
	metrics.SuccessRatio = adapterutil.NormalizeRatio(window.SuccessRate)
	metrics.AverageLatencyMS = averageMS
	return metrics
}

func modelProbeBucketMetrics(bucket modelProbeBucket) domain.Metrics {
	metrics := domain.Metrics{}
	if bucket.Requests > 0 {
		metrics.RequestCount = int64Pointer(bucket.Requests)
		metrics.SuccessCount = int64Pointer(bucket.Success)
		failure := bucket.Requests - bucket.Success
		if failure < 0 {
			failure = 0
		}
		metrics.FailureCount = int64Pointer(failure)
	}
	metrics.SuccessRatio = adapterutil.NormalizeRatio(bucket.SuccessRate)
	return metrics
}

func modelProbeState(status string, ratio *float64) domain.ServiceState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "available", "healthy", "up":
		return domain.ServiceHealthy
	case "degraded", "warning":
		return domain.ServiceDegraded
	case "unavailable", "failed", "down", "error":
		return domain.ServiceFailed
	case "not_probed", "unknown", "":
		return serviceState("", adapterutil.NormalizeRatio(ratio))
	default:
		return serviceState(status, adapterutil.NormalizeRatio(ratio))
	}
}
