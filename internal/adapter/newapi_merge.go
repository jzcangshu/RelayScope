package adapter

import (
	"fmt"
	"math"
	"net/url"
	"relayscope/internal/adapter/adapterutil"
	"relayscope/internal/domain"
	"relayscope/internal/routing"
	"sort"
	"strconv"
	"strings"
	"time"
)

func mergeDetailBuckets(model *domain.ModelObservation, buckets []detailBucket, now time.Time) {
	mergeDetailBucketsAt(model, buckets, now, time.Now().UTC())
}

func mergeDetailBucketsAt(model *domain.ModelObservation, buckets []detailBucket, now, collectedAt time.Time) {
	groups := make(map[string]int, len(model.Groups))
	for index, group := range model.Groups {
		groups[group.RawName] = index
	}
	ungroupedTargets := []string{"default"}
	if _, hasDefault := groups["default"]; !hasDefault && len(groups) > 0 {
		ungroupedTargets = make([]string, 0, len(groups))
		for name := range groups {
			ungroupedTargets = append(ungroupedTargets, name)
		}
		sort.Strings(ungroupedTargets)
	}
	targetGroups := func(groupName string) []string {
		if strings.TrimSpace(groupName) != "" {
			return []string{groupName}
		}
		return ungroupedTargets
	}
	startsByGroup := make(map[string][]time.Time)
	for _, item := range buckets {
		if item.Aggregate {
			continue
		}
		if start, ok := detailBucketStart(item); ok {
			for _, groupName := range targetGroups(item.Group) {
				startsByGroup[groupName] = append(startsByGroup[groupName], start)
			}
		}
	}
	for groupName := range startsByGroup {
		sort.SliceStable(startsByGroup[groupName], func(i, j int) bool {
			return startsByGroup[groupName][i].Before(startsByGroup[groupName][j])
		})
	}
	ensureGroup := func(groupName string) *domain.GroupObservation {
		index, exists := groups[groupName]
		if !exists {
			index = len(model.Groups)
			groups[groupName] = index
			model.Groups = append(model.Groups, domain.GroupObservation{RawName: groupName, ServiceState: domain.ServiceNoSamples})
		}
		return &model.Groups[index]
	}
	aggregateGroups := make(map[string]struct{})

	// Group-level fields are window aggregates. Keep them as 24-hour metrics,
	// but never present them as the current state.
	for _, item := range buckets {
		if !item.Aggregate {
			continue
		}
		for _, groupName := range targetGroups(item.Group) {
			group := ensureGroup(groupName)
			aggregateGroups[groupName] = struct{}{}
			group.ServiceState = domain.ServiceNoSamples
			group.ObservedAt = time.Time{}
			group.Metrics = domain.Metrics{RequestCount: item.Requests, SuccessCount: item.Success, FailureCount: item.Failure, EmptyCount: item.Empty, SuccessRatio: adapterutil.NormalizeRatio(item.SuccessRate), AverageLatencyMS: item.Latency, FirstTokenMS: item.TTFT, TokensPerSecond: item.TPS}
		}
	}

	// Series points are actual observations and own the current state.
	type windowTotals struct {
		requests, success, failure, empty                       int64
		ratioNumerator                                          float64
		ratioRequests                                           int64
		hasRequests, hasSuccess, hasFailure, hasEmpty, hasRatio bool
	}
	totalsByGroup := make(map[string]*windowTotals)
	metricStart := now.UTC().Add(-24 * time.Hour)
	for _, item := range buckets {
		if item.Aggregate {
			continue
		}
		bucketMetrics := domain.Metrics{RequestCount: item.Requests, SuccessCount: item.Success, FailureCount: item.Failure, EmptyCount: item.Empty, SuccessRatio: adapterutil.NormalizeRatio(item.SuccessRate), AverageLatencyMS: item.Latency, FirstTokenMS: item.TTFT, TokensPerSecond: item.TPS}
		start, ok := detailBucketStart(item)
		if !ok {
			start = now.UTC().Add(-5 * time.Minute)
		}
		end := detailBucketEnd(item)
		for _, groupName := range targetGroups(item.Group) {
			groupEnd := end
			if groupEnd.IsZero() {
				starts := startsByGroup[groupName]
				resolution := 5 * time.Minute
				if ok {
					position := sort.Search(len(starts), func(index int) bool { return !starts[index].Before(start) })
					if position+1 < len(starts) && starts[position+1].After(start) {
						resolution = starts[position+1].Sub(start)
					} else if position > 0 && starts[position-1].Before(start) {
						resolution = start.Sub(starts[position-1])
					}
				}
				if resolution > time.Hour {
					resolution = time.Hour
				}
				groupEnd = start.Add(resolution)
			}
			if !groupEnd.After(start) {
				groupEnd = start.Add(5 * time.Minute)
			}
			group := ensureGroup(groupName)
			group.Buckets = append(group.Buckets, domain.TimeBucket{Start: start, End: groupEnd, Resolution: groupEnd.Sub(start), Metrics: bucketMetrics})
			if groupEnd.After(metricStart) && start.Before(now.UTC()) && item.Requests != nil {
				totals := totalsByGroup[groupName]
				if totals == nil {
					totals = &windowTotals{}
					totalsByGroup[groupName] = totals
				}
				totals.requests += *item.Requests
				totals.hasRequests = true
				if item.Success != nil {
					totals.success += *item.Success
					totals.hasSuccess = true
					totals.ratioNumerator += float64(*item.Success)
					totals.ratioRequests += *item.Requests
					totals.hasRatio = true
				} else if ratio := adapterutil.NormalizeRatio(item.SuccessRate); ratio != nil {
					totals.ratioNumerator += *ratio * float64(*item.Requests)
					totals.ratioRequests += *item.Requests
					totals.hasRatio = true
				}
				if item.Failure != nil {
					totals.failure += *item.Failure
					totals.hasFailure = true
				}
				if item.Empty != nil {
					totals.empty += *item.Empty
					totals.hasEmpty = true
				}
			}
			if detailBucketOwnsCurrent(item) && (group.ObservedAt.IsZero() || start.After(group.ObservedAt)) {
				group.ObservedAt = start
				group.ServiceState = serviceState("", bucketMetrics.SuccessRatio)
			}
		}
	}
	for groupName, totals := range totalsByGroup {
		if _, hasAggregate := aggregateGroups[groupName]; hasAggregate {
			continue
		}
		metrics := domain.Metrics{}
		if totals.hasRequests {
			metrics.RequestCount = int64Pointer(totals.requests)
		}
		if totals.hasSuccess {
			metrics.SuccessCount = int64Pointer(totals.success)
		}
		if totals.hasFailure {
			metrics.FailureCount = int64Pointer(totals.failure)
		}
		if totals.hasEmpty {
			metrics.EmptyCount = int64Pointer(totals.empty)
		}
		if totals.ratioRequests > 0 && totals.hasRatio {
			ratio := totals.ratioNumerator / float64(totals.ratioRequests)
			metrics.SuccessRatio = &ratio
		}
		ensureGroup(groupName).Metrics = metrics
	}
	recordRoutingDetailEvidence(model, buckets, collectedAt)
}

// Keep the actual latest source window separate from the display's synthetic
// intervals and 24-hour aggregate. Missing protocol evidence stays unknown.
func recordRoutingDetailEvidence(model *domain.ModelObservation, buckets []detailBucket, collectedAt time.Time) {
	for index := range model.Groups {
		group := &model.Groups[index]
		var latest *detailBucket
		var latestStart time.Time
		copied := false
		for bucketIndex := range buckets {
			item := &buckets[bucketIndex]
			if item.Aggregate || (item.Group != "" && item.Group != group.RawName) {
				continue
			}
			sourceItem := *item
			if item.SourceTimestamp != 0 {
				sourceItem.Timestamp, sourceItem.EndTimestamp = item.SourceTimestamp, item.SourceEndTimestamp
			}
			start, valid := detailBucketStart(sourceItem)
			if !valid || !detailBucketOwnsCurrent(*item) {
				continue
			}
			if latest == nil || start.After(latestStart) {
				latest, latestStart, copied = item, start, item.Group == ""
			} else if start.Equal(latestStart) && item.Group == "" {
				copied = true
			}
		}
		if latest == nil {
			continue
		}
		metadata := routing.ReadExtension(group.Extension)
		availability := routing.UnknownAvailability()
		availability.Evidence = "group-observed"
		if copied {
			availability.Evidence = "copied"
		} else {
			known := true
			metadata.GroupKnown = &known
		}
		availability.CollectionStatus = "ok"
		at := collectedAt.UnixMilli()
		availability.CollectedAt = &at
		endTime := detailBucketEnd(*latest)
		if latest.SourceTimestamp != 0 {
			endTime = adapterutil.ParseFlexibleTime(latest.SourceEndTimestamp)
		}
		start, end := latestStart.UnixMilli(), endTime.UnixMilli()
		if end > start {
			availability.WindowStartedAt, availability.WindowEndedAt, availability.SourceObservedAt = &start, &end, &end
		}
		availability.SampleCount = latest.Requests
		availability.SuccessRate = routingSuccessRate(*latest)
		if availability.SuccessRate != nil && latest.Requests != nil && *latest.Requests > 0 {
			availability.Status = "available"
			if *availability.SuccessRate < 0.5 {
				availability.Status = "unavailable"
			}
		}
		metadata.Availability = &availability
		group.Extension = routing.WriteExtension(group.Extension, metadata)
	}
}

// Preserve incomplete rates for display, but never hide contradictory source
// counts behind a different rate. Empty/failure counts may overlap, so they are
// bounded independently rather than guessed into a total.
func routingSuccessRate(item detailBucket) *float64 {
	for _, count := range []*int64{item.Requests, item.Success, item.Failure, item.Empty} {
		if count != nil && (*count < 0 || (item.Requests != nil && *count > *item.Requests)) {
			return nil
		}
	}
	if item.Requests != nil && *item.Requests == 0 {
		return nil
	}
	rate := adapterutil.NormalizeRatio(item.SuccessRate)
	if rate != nil && (math.IsNaN(*rate) || math.IsInf(*rate, 0) || *rate < 0 || *rate > 1) {
		return nil
	}
	if item.Requests != nil && *item.Requests > 0 && item.Success != nil {
		countRate := float64(*item.Success) / float64(*item.Requests)
		// Only allow representation noise in float-derived source ratios.
		if rate != nil && math.Abs(*rate-countRate) > 1e-12 {
			return nil
		}
		return &countRate
	}
	return rate
}

func detailBucketOwnsCurrent(item detailBucket) bool {
	if item.Complete == nil || *item.Complete {
		return true
	}
	return item.SuccessRate != nil || item.Requests == nil || *item.Requests > 0
}

func detailBucketStart(item detailBucket) (time.Time, bool) {
	if item.Timestamp != 0 {
		return adapterutil.ParseFlexibleTime(item.Timestamp), true
	}
	if item.Time != "" {
		if parsed, err := time.Parse(time.RFC3339, item.Time); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func detailBucketEnd(item detailBucket) time.Time {
	if item.EndTimestamp != 0 {
		return adapterutil.ParseFlexibleTime(item.EndTimestamp)
	}
	if item.EndTime != "" {
		if parsed, err := time.Parse(time.RFC3339, item.EndTime); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func resolveSiteURL(base, path string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse site base URL: %w", err)
	}
	pathURL, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("parse adapter path: %w", err)
	}
	return baseURL.ResolveReference(pathURL).String(), nil
}

func serviceState(status string, ratio *float64) domain.ServiceState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "down", "failed", "offline", "error":
		return domain.ServiceFailed
	case "degraded", "warning":
		return domain.ServiceDegraded
	}
	return adapterutil.RatioToServiceState(ratio)
}

func metricsFromPricing(item pricingModel) domain.Metrics {
	return domain.Metrics{SuccessRatio: adapterutil.NormalizeRatio(item.SuccessRate), AverageLatencyMS: item.Latency, TokensPerSecond: item.TPS}
}

func deduplicateModels(models []domain.ModelObservation) []domain.ModelObservation {
	byName := make(map[string]int, len(models))
	result := make([]domain.ModelObservation, 0, len(models))
	for _, model := range models {
		index, exists := byName[model.RawName]
		if !exists {
			byName[model.RawName] = len(result)
			result = append(result, model)
			continue
		}
		mergeModelHistoryCoverage(&result[index], model)
		groupsByName := make(map[string]struct{}, len(result[index].Groups))
		for _, group := range result[index].Groups {
			groupsByName[group.RawName] = struct{}{}
		}
		for _, group := range model.Groups {
			if _, exists := groupsByName[group.RawName]; exists {
				continue
			}
			groupsByName[group.RawName] = struct{}{}
			result[index].Groups = append(result[index].Groups, group)
		}
	}
	return result
}

func mergeModelHistoryCoverage(target *domain.ModelObservation, incoming domain.ModelObservation) {
	if target.HistoryCoverageStart.IsZero() || target.HistoryCoverageEnd.IsZero() || incoming.HistoryCoverageStart.IsZero() || incoming.HistoryCoverageEnd.IsZero() {
		target.HistoryCoverageStart = time.Time{}
		target.HistoryCoverageEnd = time.Time{}
		return
	}
	if incoming.HistoryCoverageStart.After(target.HistoryCoverageStart) {
		target.HistoryCoverageStart = incoming.HistoryCoverageStart
	}
	if incoming.HistoryCoverageEnd.Before(target.HistoryCoverageEnd) {
		target.HistoryCoverageEnd = incoming.HistoryCoverageEnd
	}
}

func detailQuery(path, model string, hours int) string {
	query := url.Values{}
	query.Set("model", model)
	query.Set("hours", strconv.Itoa(hours))
	return path + "?" + query.Encode()
}
