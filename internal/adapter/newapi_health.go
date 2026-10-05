package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"relayscope/internal/adapter/adapterutil"
	"relayscope/internal/domain"
)

// modelHealthAvailability is a single-request availability source that carries
// hourly traffic buckets for every model. Sites whose perf-metrics pipeline
// lags hours behind their own dashboard expose this endpoint with current
// data, so when availabilityPath is configured it replaces the summary
// preflight and per-model detail fan-out entirely; a failed request fails the
// run rather than silently keeping the stale source.
type modelHealthResponse struct {
	Success *bool `json:"success"`
	Data    struct {
		WindowHours int                `json:"window_hours"`
		Models      []modelHealthModel `json:"models"`
	} `json:"data"`
}

type modelHealthModel struct {
	ModelName           string              `json:"model_name"`
	AverageLatencyMS    *float64            `json:"avg_latency_ms"`
	AverageFirstTokenMS *float64            `json:"avg_first_token_ms"`
	Buckets             []modelHealthBucket `json:"buckets"`
}

type modelHealthBucket struct {
	Hour         int64    `json:"hour"`
	TotalCount   *int64   `json:"total_count"`
	SuccessCount *int64   `json:"success_count"`
	ProbeCount   *int64   `json:"probe_count"`
	SuccessRate  *float64 `json:"success_rate"`
}

func collectModelHealthAvailability(
	ctx context.Context,
	fetcher Fetcher,
	collection *domain.Collection,
	baseURL, availabilityPath string,
	windowHours int,
	modelNames []string,
	now time.Time,
	completionTime func() time.Time,
) error {
	if collection == nil || len(modelNames) == 0 {
		return nil
	}
	endpoint, err := resolveSiteURL(baseURL, availabilityPath)
	if err != nil {
		return err
	}
	healthURL, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse model-health endpoint: %w", err)
	}
	query := healthURL.Query()
	query.Set("hours", strconv.Itoa(windowHours))
	healthURL.RawQuery = query.Encode()
	body, _, err := fetcher.GetBytes(ctx, healthURL.String())
	if err != nil {
		return err
	}
	var response modelHealthResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode model-health: %w", err)
	}
	if (response.Success != nil && !*response.Success) || response.Data.Models == nil {
		return errors.New("model-health endpoint reported no data")
	}
	matched := make(map[string]struct{}, len(modelNames))
	for _, name := range modelNames {
		matched[name] = struct{}{}
	}
	completedAt := now.UTC()
	if completionTime != nil {
		completedAt = completionTime()
	}
	coverageStart := now.UTC().Add(-time.Duration(windowHours) * time.Hour)
	for index := range collection.Models {
		model := &collection.Models[index]
		if _, ok := matched[model.RawName]; !ok {
			continue
		}
		var health *modelHealthModel
		for position := range response.Data.Models {
			if response.Data.Models[position].ModelName == model.RawName {
				health = &response.Data.Models[position]
				break
			}
		}
		if health == nil {
			continue
		}
		buckets := modelHealthBuckets(*health)
		if len(buckets) == 0 {
			continue
		}
		model.HistoryCoverageStart = coverageStart
		model.HistoryCoverageEnd = now.UTC()
		mergeDetailBucketsAt(model, buckets, now, completedAt)
	}
	return nil
}

// modelHealthBuckets converts model-health entries into detail buckets. Quiet
// hours (no traffic and no probes) are dropped so an idle current hour cannot
// overwrite the state carried by the latest active hour, and one aggregate
// entry carries the model-level window totals and latency averages that the
// source only reports per model. Group names are left empty: the merge copies
// model-level evidence onto every group the pricing catalog lists.
func modelHealthBuckets(health modelHealthModel) []detailBucket {
	buckets := make([]detailBucket, 0, len(health.Buckets)+1)
	var totalRequests, totalSuccess int64
	for _, item := range health.Buckets {
		if item.Hour <= 0 {
			continue
		}
		active := (item.TotalCount != nil && *item.TotalCount > 0) || (item.ProbeCount != nil && *item.ProbeCount > 0)
		if !active {
			continue
		}
		bucket := detailBucket{Timestamp: item.Hour}
		if item.TotalCount != nil && *item.TotalCount > 0 {
			bucket.Requests = item.TotalCount
			totalRequests += *item.TotalCount
		}
		if item.SuccessCount != nil && *item.SuccessCount >= 0 {
			bucket.Success = item.SuccessCount
			totalSuccess += *item.SuccessCount
		}
		bucket.SuccessRate = adapterutil.NormalizeRatio(item.SuccessRate)
		buckets = append(buckets, bucket)
	}
	if len(buckets) == 0 {
		return nil
	}
	aggregate := detailBucket{Aggregate: true}
	if totalRequests > 0 {
		requests, success := totalRequests, totalSuccess
		aggregate.Requests = &requests
		aggregate.Success = &success
		rate := float64(totalSuccess) / float64(totalRequests)
		aggregate.SuccessRate = &rate
	}
	aggregate.Latency = health.AverageLatencyMS
	aggregate.TTFT = health.AverageFirstTokenMS
	return append(buckets, aggregate)
}
