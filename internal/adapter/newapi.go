package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"relaypulse/internal/adapter/adapterutil"
	"relaypulse/internal/domain"
	"relaypulse/internal/pricing"
)

type NewAPIConfig struct {
	PricingPath       string `json:"pricingPath"`
	SummaryPath       string `json:"summaryPath"`
	DetailPath        string `json:"detailPath"`
	WindowHours       int    `json:"windowHours"`
	CatalogPageSize   int    `json:"catalogPageSize"`
	SkipDetails       bool   `json:"skipDetails"`
	PricingAdapter    string `json:"pricingAdapter"`
	PricingStatusPath string `json:"pricingStatusPath"`
	AvailabilityMode  string `json:"availabilityMode"`
	PricingNeedsLogin bool   `json:"pricingRequiresSession"`
}

type NewAPIAdapter struct {
	PricingRegistry *pricing.Registry
}

func (NewAPIAdapter) Key() string         { return "newapi-pricing" }
func (NewAPIAdapter) DisplayName() string { return "NewAPI 模型广场" }
func (NewAPIAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pricingPath":{"type":"string","default":"/api/pricing"},"summaryPath":{"type":"string","default":"/api/perf-metrics/summary"},"detailPath":{"type":"string","default":"/api/perf-metrics"},"windowHours":{"type":"integer","minimum":1,"maximum":72,"default":24},"catalogPageSize":{"type":"integer","minimum":1,"maximum":1000,"default":1000},"skipDetails":{"type":"boolean","default":false},"pricingAdapter":{"type":"string","default":"newapi"},"pricingStatusPath":{"type":"string","default":"/api/status"},"availabilityMode":{"type":"string","enum":["metrics","presence"]},"pricingRequiresSession":{"type":"boolean"}}}`)
}

type pricingModel struct {
	Model          string   `json:"model"`
	Provider       string   `json:"provider"`
	Group          string   `json:"group"`
	Groups         []string `json:"groups"`
	Channel        string   `json:"channel"`
	Status         string   `json:"status"`
	SuccessRate    *float64 `json:"success_rate"`
	Latency        *float64 `json:"latency"`
	TPS            *float64 `json:"tps"`
	Buckets        []detailBucket
	HistoryPresent bool
}

type summaryResponse struct {
	Data []summaryModel `json:"data"`
}

type summaryModel struct {
	Model       string   `json:"model"`
	Provider    string   `json:"provider"`
	Group       string   `json:"group"`
	SuccessRate *float64 `json:"success_rate"`
	Latency     *float64 `json:"latency"`
	TPS         *float64 `json:"tps"`
	Requests    *int64   `json:"requests"`
	Success     *int64   `json:"success"`
	Failure     *int64   `json:"failure"`
}

type detailBucket struct {
	Aggregate    bool
	Complete     *bool
	Timestamp    int64    `json:"timestamp"`
	EndTimestamp int64    `json:"end_timestamp"`
	Time         string   `json:"time"`
	EndTime      string   `json:"end_time"`
	Group        string   `json:"group"`
	SuccessRate  *float64 `json:"success_rate"`
	Latency      *float64 `json:"latency"`
	TTFT         *float64 `json:"ttft"`
	TPS          *float64 `json:"tps"`
	Requests     *int64   `json:"requests"`
	Success      *int64   `json:"success"`
	Failure      *int64   `json:"failure"`
	Empty        *int64   `json:"empty"`
}

func (adapter NewAPIAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return domain.Collection{}, fmt.Errorf("apply NewAPI config defaults: %w", err)
	}
	var config NewAPIConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return domain.Collection{}, fmt.Errorf("decode NewAPI config: %w", err)
	}
	if config.WindowHours <= 0 {
		config.WindowHours = 24
	}
	availabilityMode := strings.ToLower(strings.TrimSpace(config.AvailabilityMode))
	if availabilityMode != "" && availabilityMode != "metrics" && availabilityMode != "presence" {
		return domain.Collection{}, fmt.Errorf("unsupported NewAPI availability mode %q", config.AvailabilityMode)
	}
	pricingURL, err := resolveSiteURL(site.BaseURL, config.PricingPath)
	if err != nil {
		return domain.Collection{}, err
	}
	body, _, err := fetcher.GetBytes(ctx, pricingURL)
	if err != nil {
		return domain.Collection{}, err
	}
	models, err := decodePricingModels(body)
	if err != nil {
		return domain.Collection{}, fmt.Errorf("decode NewAPI pricing: %w", err)
	}
	if len(models) == 0 {
		return domain.Collection{}, fmt.Errorf("NewAPI pricing returned no models")
	}
	pricingRegistry := adapter.PricingRegistry
	if pricingRegistry == nil {
		pricingRegistry = pricing.DefaultRegistry()
	}
	priceCatalog := pricing.Catalog{}
	if config.PricingAdapter != "" {
		var statusBody []byte
		if config.PricingStatusPath != "" {
			statusURL, resolveStatusErr := resolveSiteURL(site.BaseURL, config.PricingStatusPath)
			if resolveStatusErr == nil {
				statusBody, _, _ = fetcher.GetBytes(ctx, statusURL)
			}
		}
		decoded, decodeErr := pricingRegistry.Decode(config.PricingAdapter, body, statusBody)
		if decodeErr != nil {
			return domain.Collection{}, fmt.Errorf("decode pricing metadata: %w", decodeErr)
		}
		priceCatalog = decoded
	}

	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true}
	if availabilityMode == "presence" {
		collection.MissingCatalogState = domain.ServiceFailed
	}
	for _, item := range models {
		if strings.TrimSpace(item.Model) == "" {
			continue
		}
		groupNames := item.Groups
		if len(groupNames) == 0 {
			groupName := item.Group
			if groupName == "" {
				groupName = item.Channel
			}
			if groupName == "" {
				groupName = "default"
			}
			groupNames = []string{groupName}
		}
		groups := make([]domain.GroupObservation, 0, len(groupNames))
		for _, groupName := range groupNames {
			state := serviceState(item.Status, adapterutil.NormalizeRatio(item.SuccessRate))
			if availabilityMode == "presence" {
				state = domain.ServiceHealthy
			}
			groups = append(groups, domain.GroupObservation{RawName: groupName, ServiceState: state, Metrics: metricsFromPricing(item)})
		}
		observation := domain.ModelObservation{
			RawName:  item.Model,
			Provider: item.Provider,
			Groups:   groups,
		}
		if len(item.Buckets) > 0 {
			mergeDetailBuckets(&observation, item.Buckets, now)
		}
		collection.Models = append(collection.Models, observation)
	}
	collection.Models = deduplicateModels(collection.Models)
	applyPricingCatalog(&collection, priceCatalog)
	if len(collection.Models) == 0 {
		return domain.Collection{}, fmt.Errorf("NewAPI pricing contained no valid model names")
	}
	return collection, nil
}

func (adapter NewAPIAdapter) CollectDetails(ctx context.Context, site Site, fetcher Fetcher, collection *domain.Collection, modelNames []string, now time.Time) error {
	if collection == nil || len(modelNames) == 0 {
		return nil
	}
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return fmt.Errorf("apply NewAPI config defaults: %w", err)
	}
	var config NewAPIConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return fmt.Errorf("decode NewAPI detail config: %w", err)
	}
	if config.WindowHours <= 0 {
		config.WindowHours = 24
	}
	if config.SkipDetails {
		return nil
	}
	endpoint, err := resolveSiteURL(site.BaseURL, config.DetailPath)
	if err != nil {
		return err
	}
	return collectModelDetails(ctx, fetcher, collection, modelNames, now, time.Duration(config.WindowHours)*time.Hour, func(modelName string) (string, error) {
		return detailQuery(endpoint, modelName, config.WindowHours), nil
	})
}

func collectModelDetails(
	ctx context.Context,
	fetcher Fetcher,
	collection *domain.Collection,
	modelNames []string,
	now time.Time,
	historyWindow time.Duration,
	endpointFor func(string) (string, error),
) error {
	byModel := make(map[string]*domain.ModelObservation, len(collection.Models))
	for index := range collection.Models {
		byModel[collection.Models[index].RawName] = &collection.Models[index]
	}
	attempted := 0
	succeeded := 0
	issueStart := len(collection.Issues)
	for _, modelName := range modelNames {
		model := byModel[modelName]
		if model == nil {
			continue
		}
		attempted++
		endpoint, err := endpointFor(modelName)
		if err != nil {
			appendDetailIssue(collection, "detail_url_failed", modelName, err)
			continue
		}
		body, _, err := fetcher.GetBytes(ctx, endpoint)
		if err != nil {
			appendDetailIssue(collection, "detail_fetch_failed", modelName, err)
			continue
		}
		buckets, err := decodeDetailBuckets(body)
		if err != nil {
			appendDetailIssue(collection, "detail_decode_failed", modelName, err)
			continue
		}
		if historyWindow > 0 {
			model.HistoryCoverageStart = now.UTC().Add(-historyWindow)
			model.HistoryCoverageEnd = now.UTC()
		}
		mergeDetailBuckets(model, buckets, now)
		succeeded++
	}
	if attempted > 0 && succeeded == 0 && len(collection.Issues) > issueStart {
		return fmt.Errorf("all %d detail requests failed: %s", attempted, collection.Issues[issueStart].Message)
	}
	return nil
}

func appendDetailIssue(collection *domain.Collection, code, modelName string, err error) {
	message := "detail request failed"
	switch code {
	case "detail_url_failed":
		message = "detail URL is invalid"
	case "detail_fetch_failed":
		var fetchErr *FetchError
		if errors.As(err, &fetchErr) && fetchErr.StatusCode > 0 {
			message = fmt.Sprintf("detail request returned HTTP %d", fetchErr.StatusCode)
		} else if errors.Is(err, context.DeadlineExceeded) {
			message = "detail request timed out"
		} else if errors.Is(err, context.Canceled) {
			message = "detail request was canceled"
		}
	case "detail_decode_failed":
		message = strings.Join(strings.Fields(err.Error()), " ")
	}
	runes := []rune(message)
	if len(runes) > 512 {
		message = string(runes[:512])
	}
	collection.Issues = append(collection.Issues, domain.CollectionIssue{Code: code, Scope: modelName, Message: message})
}

// decodePricingModels accepts the response shapes used by NewAPI versions and
// by compatible public model pages: a top-level array, data/models arrays, or
// one level of result/payload wrapping. Unknown objects are ignored.
func decodePricingModels(body []byte) ([]pricingModel, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	items := findArray(value, 0)
	// Some embedded NewAPI probes expose the selected catalog as a plain
	// string array rather than model objects.
	if len(items) == 0 {
		if names := findStringArray(value, 0); len(names) > 0 {
			items = make([]any, 0, len(names))
			for _, name := range names {
				items = append(items, map[string]any{"model": name})
			}
		}
	}
	models := make([]pricingModel, 0, len(items))
	for _, item := range items {
		if name, ok := item.(string); ok {
			if name = strings.TrimSpace(name); name != "" {
				models = append(models, pricingModel{Model: name})
			}
			continue
		}
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		model := pricingModel{
			Model:    firstString(object, "model", "model_name", "name", "id"),
			Provider: firstString(object, "provider", "supplier", "vendor"),
			Group:    firstString(object, "group", "group_name", "channel_name", "channel"),
			Channel:  firstString(object, "channel"),
			Status:   firstString(object, "status", "state"),
		}
		if channel, ok := object["channel"].(map[string]any); ok {
			if model.Group == "" {
				model.Group = firstString(channel, "name", "group", "slug")
			}
			if model.Provider == "" {
				model.Provider = firstString(channel, "provider", "type")
			}
		}
		if health, ok := object["health"].(map[string]any); ok {
			if current, ok := health["current"].(map[string]any); ok {
				model.SuccessRate = numberPointer(current, "success_rate", "successRate")
				model.Latency = numberPointer(current, "latency_ms", "avg_latency_ms", "latency")
			}
			_, model.HistoryPresent = health["buckets"]
			model.Buckets = decodeEmbeddedHealthBuckets(health, model.Group)
		}
		model.Groups = stringSlice(object, "enable_groups", "groups", "group_names")
		if value := numberPointer(object, "success_rate", "successRate", "success_ratio", "successRatio"); value != nil {
			model.SuccessRate = value
		}
		if value := numberPointer(object, "latency", "avg_latency", "avg_latency_ms", "average_latency", "average_latency_ms"); value != nil {
			model.Latency = value
		}
		model.TPS = numberPointer(object, "tps", "avg_tps", "tokens_per_second", "tokensPerSecond")
		if strings.TrimSpace(model.Model) != "" {
			models = append(models, model)
		}
	}
	return models, nil
}

func decodeEmbeddedHealthBuckets(health map[string]any, groupName string) []detailBucket {
	rawBuckets, ok := health["buckets"].([]any)
	if !ok {
		return nil
	}
	buckets := make([]detailBucket, 0, len(rawBuckets))
	for _, rawBucket := range rawBuckets {
		object, ok := rawBucket.(map[string]any)
		if !ok {
			continue
		}
		bucket := detailBucket{
			Complete:    boolPointer(object, "is_complete", "complete"),
			Timestamp:   int64Value(object, "timestamp", "ts", "time_unix"),
			Time:        firstString(object, "bucket_start", "start", "time", "timestamp_iso"),
			EndTime:     firstString(object, "bucket_end", "end", "end_time", "end_time_iso"),
			Group:       groupName,
			SuccessRate: numberPointer(object, "success_rate", "successRate", "success_ratio", "successRatio"),
			Latency:     numberPointer(object, "latency_ms", "avg_latency_ms", "latency"),
		}
		bucket.Requests = intPointer(object, "sample_count", "total_requests", "requests", "request_count")
		bucket.Success = intPointer(object, "success_count", "success", "successCount")
		bucket.Failure = intPointer(object, "failure_count", "failure", "failureCount")
		bucket.Empty = intPointer(object, "empty_count", "empty", "emptyCount")
		if bucket.Requests == nil && (bucket.Success != nil || bucket.Failure != nil) {
			requests := int64(0)
			if bucket.Success != nil {
				requests += *bucket.Success
			}
			if bucket.Failure != nil {
				requests += *bucket.Failure
			}
			bucket.Requests = &requests
		}
		if bucket.Timestamp != 0 || bucket.Time != "" {
			buckets = append(buckets, bucket)
		}
	}
	return buckets
}

func findStringArray(value any, depth int) []string {
	if depth > 4 {
		return nil
	}
	if items, ok := value.([]any); ok {
		result := make([]string, 0, len(items))
		for _, item := range items {
			name, ok := item.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return nil
			}
			result = append(result, strings.TrimSpace(name))
		}
		return result
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"data", "models", "items", "records", "result", "payload"} {
		if child, exists := object[key]; exists {
			if result := findStringArray(child, depth+1); len(result) > 0 {
				return result
			}
		}
	}
	return nil
}

func stringSlice(object map[string]any, keys ...string) []string {
	for _, key := range keys {
		items, ok := object[key].([]any)
		if !ok {
			continue
		}
		result := make([]string, 0, len(items))
		for _, item := range items {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				result = append(result, strings.TrimSpace(value))
			}
		}
		return result
	}
	return nil
}

func decodeDetailBuckets(body []byte) ([]detailBucket, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	if probeBuckets := decodeProbeStatus(value); len(probeBuckets) > 0 {
		return probeBuckets, nil
	}
	if groups := findGroups(value, 0); len(groups) > 0 {
		return decodeNewAPIGroups(groups), nil
	}
	items := findArray(value, 0)
	buckets := make([]detailBucket, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		bucket := detailBucket{
			Timestamp:    int64Value(object, "timestamp", "ts", "time_unix"),
			EndTimestamp: int64Value(object, "end_timestamp", "end_ts", "end_time"),
			Time:         firstString(object, "time", "timestamp_iso", "start", "bucket_start"),
			EndTime:      firstString(object, "end", "bucket_end", "end_time", "end_time_iso"),
			Group:        firstString(object, "group", "group_name", "channel", "channel_name", "token_group", "tokenGroup"),
			SuccessRate:  numberPointer(object, "success_rate", "successRate", "success_ratio", "successRatio"),
			Latency:      numberPointer(object, "latency", "avg_latency", "avg_latency_ms", "average_latency", "average_latency_ms"),
			TTFT:         numberPointer(object, "ttft", "avg_ttft_ms", "first_token_ms", "firstTokenMs"),
			TPS:          numberPointer(object, "tps", "avg_tps", "tokens_per_second", "tokensPerSecond"),
		}
		bucket.Requests = intPointer(object, "requests", "request_count", "requestCount")
		bucket.Success = intPointer(object, "success", "success_count", "successCount")
		bucket.Failure = intPointer(object, "failure", "failure_count", "failureCount")
		bucket.Empty = intPointer(object, "empty", "empty_count", "emptyCount")
		if bucket.Timestamp != 0 || bucket.Time != "" || bucket.SuccessRate != nil || bucket.Latency != nil {
			buckets = append(buckets, bucket)
		}
	}
	return buckets, nil
}

func decodeProbeStatus(value any) []detailBucket {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if nested, ok := object["data"].(map[string]any); ok {
		object = nested
	}
	slots, ok := object["slot_data"].([]any)
	if !ok {
		return nil
	}
	result := []detailBucket{{
		Aggregate:   true,
		SuccessRate: numberPointer(object, "success_rate"),
		Requests:    intPointer(object, "total_requests"),
		Success:     intPointer(object, "success_count"),
		Failure:     intPointer(object, "failure_count", "error_count"),
		Empty:       intPointer(object, "empty_count"),
	}}
	for _, rawSlot := range slots {
		slot, ok := rawSlot.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, detailBucket{
			Timestamp:    int64Value(slot, "start_time"),
			EndTimestamp: int64Value(slot, "end_time", "end_timestamp"),
			SuccessRate:  numberPointer(slot, "success_rate"),
			Requests:     intPointer(slot, "total_requests"),
			Success:      intPointer(slot, "success_count"),
			Failure:      intPointer(slot, "failure_count", "error_count"),
			Empty:        intPointer(slot, "empty_count"),
		})
	}
	return result
}

func findGroups(value any, depth int) []any {
	if depth > 5 {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if groups, ok := object["groups"].([]any); ok {
		return groups
	}
	for _, key := range []string{"data", "result", "payload"} {
		if child, exists := object[key]; exists {
			if groups := findGroups(child, depth+1); len(groups) > 0 {
				return groups
			}
		}
	}
	return nil
}

func decodeNewAPIGroups(groups []any) []detailBucket {
	result := make([]detailBucket, 0)
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		name := firstString(group, "group", "group_name", "name")
		series, _ := group["series"].([]any)
		result = append(result, detailBucket{
			Aggregate:   true,
			Group:       name,
			SuccessRate: numberPointer(group, "success_rate", "successRatio"),
			Latency:     numberPointer(group, "avg_latency_ms", "average_latency_ms", "latency"),
			TTFT:        numberPointer(group, "avg_ttft_ms", "first_token_ms", "ttft"),
			TPS:         numberPointer(group, "avg_tps", "tokens_per_second", "tps"),
		})
		for _, rawPoint := range series {
			point, ok := rawPoint.(map[string]any)
			if !ok {
				continue
			}
			result = append(result, detailBucket{
				Timestamp:    int64Value(point, "ts", "timestamp"),
				EndTimestamp: int64Value(point, "end_ts", "end_timestamp", "end_time"),
				Group:        name,
				SuccessRate:  numberPointer(point, "success_rate", "successRatio"),
				Latency:      numberPointer(point, "avg_latency_ms", "average_latency_ms", "latency"),
				TTFT:         numberPointer(point, "avg_ttft_ms", "first_token_ms", "ttft"),
				TPS:          numberPointer(point, "avg_tps", "tokens_per_second", "tps"),
			})
		}
	}
	return result
}

func findArray(value any, depth int) []any {
	if depth > 4 {
		return nil
	}
	if items, ok := value.([]any); ok {
		return items
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"data", "models", "items", "records", "result", "payload"} {
		if child, exists := object[key]; exists {
			if childObject, ok := child.(map[string]any); ok {
				for _, arrayKey := range []string{"models", "items", "records"} {
					if items, ok := childObject[arrayKey].([]any); ok {
						return items
					}
				}
			}
			if items := findArray(child, depth+1); len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"model", "model_info", "modelInfo"} {
		if nested, ok := object[key].(map[string]any); ok {
			if value := firstString(nested, "name", "model", "model_name", "id", "slug"); value != "" {
				return value
			}
		}
	}
	return ""
}

func numberPointer(object map[string]any, keys ...string) *float64 {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			switch typed := value.(type) {
			case float64:
				return &typed
			case json.Number:
				if parsed, err := typed.Float64(); err == nil {
					return &parsed
				}
			case string:
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(typed, "%")), 64); err == nil {
					if strings.HasSuffix(typed, "%") {
						parsed /= 100
					}
					return &parsed
				}
			}
		}
	}
	return nil
}

func boolPointer(object map[string]any, keys ...string) *bool {
	for _, key := range keys {
		if value, ok := object[key].(bool); ok {
			return &value
		}
	}
	return nil
}

func intPointer(object map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			var parsed int64
			switch typed := value.(type) {
			case float64:
				parsed = int64(typed)
			case string:
				parsedValue, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
				if err != nil {
					continue
				}
				parsed = parsedValue
			default:
				continue
			}
			return &parsed
		}
	}
	return nil
}

func int64Value(object map[string]any, keys ...string) int64 {
	value := intPointer(object, keys...)
	if value == nil {
		return 0
	}
	return *value
}

func mergeDetailBuckets(model *domain.ModelObservation, buckets []detailBucket, now time.Time) {
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
