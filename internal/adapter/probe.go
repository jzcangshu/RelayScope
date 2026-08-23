package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"relaypulse/internal/adapter/adapterutil"
	"relaypulse/internal/domain"
	"relaypulse/internal/pricing"
)

// ProbeAdapter handles public probe pages whose health and optional pricing
// paths are config-driven, so a new probe variant does not require a database
// migration or source-specific collector branch.
type ProbeAdapter struct {
	adapterKey        string
	display           string
	defaultPath       string
	defaultStatusPath string
	defaultDetailPath string
	paginate          bool
	inlineHistory     bool
	historyWindow     time.Duration
	PricingRegistry   *pricing.Registry
}

func (adapter ProbeAdapter) Key() string         { return adapter.adapterKey }
func (adapter ProbeAdapter) DisplayName() string { return adapter.display }
func (adapter ProbeAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"statusBaseUrl":{"type":"string"},"catalogPath":{"type":"string"},"statusPath":{"type":"string"},"detailPath":{"type":"string"},"detailPathTemplate":{"type":"string"},"pageSize":{"type":"integer","minimum":1,"maximum":200},"pricingAdapter":{"type":"string"},"pricingBaseUrl":{"type":"string"},"pricingPath":{"type":"string"},"pricingStatusPath":{"type":"string"},"pricingOptional":{"type":"boolean"},"pricingRequiresSession":{"type":"boolean"}}}`)
}

type probeConfig struct {
	StatusBaseURL      string `json:"statusBaseUrl"`
	CatalogPath        string `json:"catalogPath"`
	StatusPath         string `json:"statusPath"`
	DetailPath         string `json:"detailPath"`
	DetailPathTemplate string `json:"detailPathTemplate"`
	PageSize           int    `json:"pageSize"`
	PricingAdapter     string `json:"pricingAdapter"`
	PricingBaseURL     string `json:"pricingBaseUrl"`
	PricingPath        string `json:"pricingPath"`
	PricingStatusPath  string `json:"pricingStatusPath"`
	PricingOptional    bool   `json:"pricingOptional"`
	PricingNeedsLogin  bool   `json:"pricingRequiresSession"`
}

func (adapter ProbeAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	config := probeConfig{CatalogPath: adapter.defaultPath, StatusPath: adapter.defaultStatusPath, PageSize: 100}
	if site.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(site.ConfigJSON), &config); err != nil {
			return domain.Collection{}, fmt.Errorf("decode %s config: %w", adapter.Key(), err)
		}
	}
	path := config.CatalogPath
	if path == "" {
		path = config.StatusPath
	}
	if path == "" {
		path = adapter.defaultPath
	}
	statusBaseURL := strings.TrimSpace(config.StatusBaseURL)
	if statusBaseURL == "" {
		statusBaseURL = site.BaseURL
	}
	endpoint, err := resolveSiteURL(statusBaseURL, path)
	if err != nil {
		return domain.Collection{}, err
	}
	models, catalogBodies, err := adapter.fetchModels(ctx, endpoint, config, fetcher)
	if err != nil {
		return domain.Collection{}, fmt.Errorf("decode %s response: %w", adapter.Key(), err)
	}
	if len(models) == 0 {
		return domain.Collection{}, fmt.Errorf("%s response contained no valid model names", adapter.Key())
	}
	catalogBody := catalogBodies[0]
	if config.StatusPath != "" && config.StatusPath != path {
		statusEndpoint, resolveErr := resolveSiteURL(statusBaseURL, config.StatusPath)
		if resolveErr != nil {
			return domain.Collection{}, resolveErr
		}
		statusBody, _, statusErr := fetcher.GetBytes(ctx, statusEndpoint)
		if statusErr != nil {
			return domain.Collection{}, statusErr
		}
		statusModels, decodeErr := decodePricingModels(statusBody)
		if decodeErr != nil {
			return domain.Collection{}, decodeErr
		}
		models = mergePricingModels(models, statusModels)
	}
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true}
	for _, item := range models {
		group := item.Group
		if group == "" {
			group = item.Channel
		}
		if group == "" {
			group = "default"
		}
		observation := domain.ModelObservation{RawName: item.Model, Provider: item.Provider, Groups: []domain.GroupObservation{{RawName: group, ServiceState: serviceState(item.Status, adapterutil.NormalizeRatio(item.SuccessRate)), Metrics: metricsFromPricing(item)}}}
		if adapter.historyWindow > 0 && item.HistoryPresent {
			observation.HistoryCoverageStart = now.UTC().Add(-adapter.historyWindow)
			observation.HistoryCoverageEnd = now.UTC()
		}
		if len(item.Buckets) > 0 {
			mergeDetailBuckets(&observation, item.Buckets, now)
		}
		collection.Models = append(collection.Models, observation)
	}
	collection.Models = deduplicateModels(collection.Models)
	if adapter.inlineHistory {
		if err := mergeInlineProbeHistory(&collection, catalogBody, now); err != nil {
			return domain.Collection{}, fmt.Errorf("decode %s inline history: %w", adapter.Key(), err)
		}
	}
	collection.CatalogRawNames = make([]string, 0, len(collection.Models))
	for _, model := range collection.Models {
		collection.CatalogRawNames = append(collection.CatalogRawNames, model.RawName)
	}
	if config.PricingAdapter != "" {
		if config.PricingPath != "" {
			if err := attachPricingSource(ctx, site, fetcher, adapter.PricingRegistry, pricingSource{
				DecoderKey: config.PricingAdapter, BaseURL: config.PricingBaseURL,
				Path: config.PricingPath, StatusPath: config.PricingStatusPath, Optional: config.PricingOptional,
			}, &collection); err != nil {
				return domain.Collection{}, err
			}
		} else {
			if decodeErr := decodeAndApplyPricingBodies(adapter.PricingRegistry, config.PricingAdapter, catalogBodies, nil, &collection); decodeErr != nil && !config.PricingOptional {
				return domain.Collection{}, fmt.Errorf("decode %s catalog pricing: %w", adapter.Key(), decodeErr)
			}
		}
	}
	return collection, nil
}

func (adapter ProbeAdapter) fetchModels(ctx context.Context, endpoint string, config probeConfig, fetcher Fetcher) ([]pricingModel, [][]byte, error) {
	body, _, err := fetcher.GetBytes(ctx, endpoint)
	if err != nil {
		return nil, nil, err
	}
	models, err := decodePricingModels(body)
	if err != nil {
		return nil, nil, err
	}
	if !adapter.paginate {
		return models, [][]byte{body}, nil
	}
	pageSize := config.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}
	if len(models) < pageSize {
		return models, [][]byte{body}, nil
	}
	all := append([]pricingModel(nil), models...)
	bodies := [][]byte{body}
	for page := 2; page <= 50; page++ {
		parsed, parseErr := url.Parse(endpoint)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		query := parsed.Query()
		query.Set("page", fmt.Sprintf("%d", page))
		query.Set("page_size", fmt.Sprintf("%d", pageSize))
		parsed.RawQuery = query.Encode()
		pageBody, _, pageErr := fetcher.GetBytes(ctx, parsed.String())
		if pageErr != nil {
			return nil, nil, pageErr
		}
		pageModels, pageDecodeErr := decodePricingModels(pageBody)
		if pageDecodeErr != nil {
			return nil, nil, pageDecodeErr
		}
		if len(pageModels) == 0 {
			break
		}
		all = append(all, pageModels...)
		bodies = append(bodies, pageBody)
		if len(pageModels) < pageSize {
			break
		}
	}
	return deduplicatePricingModels(all), bodies, nil
}

type inlineProbePayload struct {
	Models []inlineProbeModel `json:"models"`
}

type inlineProbeModel struct {
	Model  string           `json:"model"`
	Health string           `json:"health"`
	LastTS int64            `json:"lastTs"`
	Bars   []inlineProbeBar `json:"bars"`
}

type inlineProbeBar struct {
	Time   int64  `json:"time"`
	Status string `json:"status"`
	Total  int64  `json:"total"`
	OK     int64  `json:"ok"`
}

func mergeInlineProbeHistory(collection *domain.Collection, body []byte, now time.Time) error {
	var payload inlineProbePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	models := make(map[string]*domain.ModelObservation, len(collection.Models))
	for index := range collection.Models {
		models[collection.Models[index].RawName] = &collection.Models[index]
	}
	retentionStart := now.UTC().Add(-72 * time.Hour)
	metricStart := now.UTC().Add(-24 * time.Hour)
	for _, source := range payload.Models {
		model := models[strings.TrimSpace(source.Model)]
		if model == nil || len(model.Groups) == 0 {
			continue
		}
		group := &model.Groups[0]
		for index := range model.Groups {
			if model.Groups[index].RawName == "default" {
				group = &model.Groups[index]
				break
			}
		}
		sort.SliceStable(source.Bars, func(left, right int) bool { return source.Bars[left].Time < source.Bars[right].Time })
		var latestStart, earliestStart time.Time
		latestState := inlineProbeHealthState(source.Health)
		var requestCount, successCount, failureCount int64
		hasWindowMetrics := false
		group.Buckets = nil
		for _, bar := range source.Bars {
			start, ok := detailBucketStart(detailBucket{Timestamp: bar.Time})
			if !ok || start.After(now.UTC()) {
				continue
			}
			if latestStart.IsZero() || start.After(latestStart) {
				latestStart = start
				latestState = inlineProbeBarState(bar)
			}
			if earliestStart.IsZero() || start.Before(earliestStart) {
				earliestStart = start
			}
			failure := bar.Total - bar.OK
			if failure < 0 {
				failure = 0
			}
			metrics := domain.Metrics{RequestCount: int64Pointer(bar.Total), SuccessCount: int64Pointer(bar.OK), FailureCount: int64Pointer(failure)}
			if strings.ToLower(strings.TrimSpace(bar.Status)) != "unknown" && bar.Total > 0 {
				ratio := float64(bar.OK) / float64(bar.Total)
				metrics.SuccessRatio = &ratio
			}
			if !start.Before(retentionStart) {
				group.Buckets = append(group.Buckets, domain.TimeBucket{Start: start, End: start.Add(5 * time.Minute), Resolution: 5 * time.Minute, Metrics: metrics})
			}
			if !start.Before(metricStart) && bar.Total > 0 {
				hasWindowMetrics = true
				requestCount += bar.Total
				successCount += bar.OK
				failureCount += failure
			}
		}
		group.ServiceState = latestState
		if source.LastTS != 0 {
			if observedAt, ok := detailBucketStart(detailBucket{Timestamp: source.LastTS}); ok {
				group.ObservedAt = observedAt
			}
		}
		if group.ObservedAt.IsZero() {
			group.ObservedAt = latestStart
		}
		group.Metrics = domain.Metrics{}
		if hasWindowMetrics && requestCount > 0 {
			ratio := float64(successCount) / float64(requestCount)
			group.Metrics = domain.Metrics{
				RequestCount: int64Pointer(requestCount), SuccessCount: int64Pointer(successCount),
				FailureCount: int64Pointer(failureCount), SuccessRatio: &ratio,
			}
		}
		if !earliestStart.IsZero() {
			if earliestStart.Before(retentionStart) {
				earliestStart = retentionStart
			}
			model.HistoryCoverageStart = earliestStart
			model.HistoryCoverageEnd = now.UTC()
		}
	}
	return nil
}

func inlineProbeHealthState(health string) domain.ServiceState {
	switch strings.ToLower(strings.TrimSpace(health)) {
	case "healthy", "up":
		return domain.ServiceHealthy
	case "degraded", "warning":
		return domain.ServiceDegraded
	case "critical", "down", "failed", "offline", "error":
		return domain.ServiceFailed
	default:
		return domain.ServiceNoSamples
	}
}

func inlineProbeBarState(bar inlineProbeBar) domain.ServiceState {
	switch strings.ToLower(strings.TrimSpace(bar.Status)) {
	case "up":
		return domain.ServiceHealthy
	case "degraded":
		return domain.ServiceDegraded
	case "down":
		return domain.ServiceFailed
	case "unknown":
		return domain.ServiceNoSamples
	default:
		if bar.Total <= 0 {
			return domain.ServiceNoSamples
		}
		ratio := float64(bar.OK) / float64(bar.Total)
		return serviceState("", &ratio)
	}
}

func int64Pointer(value int64) *int64 { return &value }

func mergePricingModels(base, extra []pricingModel) []pricingModel {
	merged := append([]pricingModel(nil), base...)
	for _, item := range extra {
		found := false
		for index := range merged {
			if merged[index].Model == item.Model && (item.Group == "" || merged[index].Group == item.Group) {
				if item.Provider != "" {
					merged[index].Provider = item.Provider
				}
				if item.Group != "" {
					merged[index].Group = item.Group
				}
				if item.Status != "" {
					merged[index].Status = item.Status
				}
				if item.SuccessRate != nil {
					merged[index].SuccessRate = item.SuccessRate
				}
				if item.Latency != nil {
					merged[index].Latency = item.Latency
				}
				if item.TPS != nil {
					merged[index].TPS = item.TPS
				}
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, item)
		}
	}
	return deduplicatePricingModels(merged)
}

func deduplicatePricingModels(items []pricingModel) []pricingModel {
	seen := make(map[string]int, len(items))
	result := make([]pricingModel, 0, len(items))
	for _, item := range items {
		key := item.Model + "\x00" + item.Group
		if index, ok := seen[key]; ok {
			if result[index].Provider == "" {
				result[index].Provider = item.Provider
			}
			continue
		}
		seen[key] = len(result)
		result = append(result, item)
	}
	return result
}

func (adapter ProbeAdapter) CollectDetails(ctx context.Context, site Site, fetcher Fetcher, collection *domain.Collection, modelNames []string, now time.Time) error {
	if collection == nil || len(modelNames) == 0 {
		return nil
	}
	config := probeConfig{DetailPathTemplate: adapter.defaultDetailPath}
	if site.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(site.ConfigJSON), &config); err != nil {
			return err
		}
	}
	path := config.DetailPath
	template := config.DetailPathTemplate
	if template != "" {
		path = template
	}
	if path == "" {
		return nil
	}
	statusBaseURL := strings.TrimSpace(config.StatusBaseURL)
	if statusBaseURL == "" {
		statusBaseURL = site.BaseURL
	}
	endpoint := ""
	if template == "" {
		var err error
		endpoint, err = resolveSiteURL(statusBaseURL, path)
		if err != nil {
			return err
		}
	}
	return collectModelDetails(ctx, fetcher, collection, modelNames, now, 24*time.Hour, func(modelName string) (string, error) {
		if template != "" {
			templatedPath := strings.ReplaceAll(template, "{model}", url.PathEscape(modelName))
			return resolveSiteURL(statusBaseURL, templatedPath)
		}
		query := url.Values{}
		query.Set("model", modelName)
		query.Set("hours", "24")
		return endpoint + "?" + query.Encode(), nil
	})
}

func NewAPIProbeAdapter() ProbeAdapter {
	return ProbeAdapter{adapterKey: "newapi-probe", display: "NewAPI 嵌入式探针", defaultPath: "/api/model-status/embed/config/selected", defaultStatusPath: "/api/model-status/embed/status/batch?window=24h", defaultDetailPath: "/api/model-status/embed/status/{model}?window=24h"}
}

func CustomProbeAdapter() ProbeAdapter {
	return ProbeAdapter{adapterKey: "custom-probe", display: "自定义状态探针", defaultPath: "/api/model-status?window=3600&recent=50", defaultStatusPath: "/api/offline-models", inlineHistory: true}
}

func ModelMarketAdapter() ProbeAdapter {
	return ProbeAdapter{adapterKey: "model-market", display: "模型市场接口", defaultPath: "/api/v1/model-market?group_by=model&sort_by=model&sort_order=asc&page=1&page_size=100&range=24h", paginate: true, historyWindow: 24 * time.Hour}
}
