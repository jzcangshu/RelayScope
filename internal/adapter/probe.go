package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"relayscope/internal/adapter/adapterutil"
	"relayscope/internal/domain"
	"relayscope/internal/pricing"
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
	defaultGroupsPath string
	defaultBatchPath  string
	paginate          bool
	inlineHistory     bool
	historyWindow     time.Duration
	PricingRegistry   *pricing.Registry
}

func (adapter ProbeAdapter) Key() string         { return adapter.adapterKey }
func (adapter ProbeAdapter) DisplayName() string { return adapter.display }
func (adapter ProbeAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"statusBaseUrl":{"type":"string"},"catalogPath":{"type":"string"},"statusPath":{"type":"string"},"detailPath":{"type":"string"},"detailPathTemplate":{"type":"string"},"groupsPath":{"type":"string"},"batchPath":{"type":"string"},"pageSize":{"type":"integer","minimum":1,"maximum":200},"pricingAdapter":{"type":"string"},"pricingBaseUrl":{"type":"string"},"pricingPath":{"type":"string"},"pricingStatusPath":{"type":"string"},"pricingOptional":{"type":"boolean"},"pricingRequiresSession":{"type":"boolean"}}}`)
}

type probeConfig struct {
	StatusBaseURL      string `json:"statusBaseUrl"`
	CatalogPath        string `json:"catalogPath"`
	StatusPath         string `json:"statusPath"`
	DetailPath         string `json:"detailPath"`
	DetailPathTemplate string `json:"detailPathTemplate"`
	GroupsPath         string `json:"groupsPath"`
	BatchPath          string `json:"batchPath"`
	PageSize           int    `json:"pageSize"`
	PricingAdapter     string `json:"pricingAdapter"`
	PricingBaseURL     string `json:"pricingBaseUrl"`
	PricingPath        string `json:"pricingPath"`
	PricingStatusPath  string `json:"pricingStatusPath"`
	PricingOptional    bool   `json:"pricingOptional"`
	PricingNeedsLogin  bool   `json:"pricingRequiresSession"`
}

func (adapter ProbeAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return domain.Collection{}, fmt.Errorf("apply %s config defaults: %w", adapter.Key(), err)
	}
	config := probeConfig{CatalogPath: adapter.defaultPath, StatusPath: adapter.defaultStatusPath, PageSize: 100}
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return domain.Collection{}, fmt.Errorf("decode %s config: %w", adapter.Key(), err)
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
	groupsPath := config.GroupsPath
	if groupsPath == "" {
		groupsPath = adapter.defaultGroupsPath
	}
	if groupsPath != "" {
		// The key-group list is what the source status page switches on; it is
		// the authoritative model-to-group mapping. Failures here only degrade
		// to the placeholder groups, so they must not abort the collection.
		if groupsURL, resolveErr := resolveSiteURL(statusBaseURL, groupsPath); resolveErr == nil {
			if groups, fetchErr := fetchProbeTokenGroups(ctx, fetcher, groupsURL); fetchErr == nil {
				applyProbeTokenGroups(&collection, groups)
			}
		}
	}
	// Catalog raw names stay the operator-curated probe selection; models added
	// from the key-group list are observations, not absence-detection anchors.
	collection.CatalogRawNames = catalogModelNames(models)
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
	batchPath := config.BatchPath
	if batchPath == "" {
		batchPath = adapter.defaultBatchPath
	}
	if batchPath != "" {
		// One batch POST carries the full 24h series for every model, so
		// CollectDetails normally has no network work left to do. Best-effort:
		// CollectDetails retries and then falls back to per-model GETs.
		if batchURL, resolveErr := resolveSiteURL(statusBaseURL, batchPath); resolveErr == nil {
			_ = mergeProbeBatchStatus(ctx, fetcher, &collection, modelRawNames(collection.Models), batchURL, now)
		}
	}
	return collection, nil
}

func modelRawNames(models []domain.ModelObservation) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		if strings.TrimSpace(model.RawName) != "" {
			names = append(names, model.RawName)
		}
	}
	return names
}

func catalogModelNames(items []pricingModel) []string {
	seen := make(map[string]struct{}, len(items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Model)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

type probeTokenGroup struct {
	GroupName string   `json:"group_name"`
	Models    []string `json:"models"`
}

func fetchProbeTokenGroups(ctx context.Context, fetcher Fetcher, groupsURL string) ([]probeTokenGroup, error) {
	body, _, err := fetcher.GetBytes(ctx, groupsURL)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []probeTokenGroup `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		var groups []probeTokenGroup
		if arrayErr := json.Unmarshal(body, &groups); arrayErr != nil {
			return nil, fmt.Errorf("decode token groups: %w", err)
		}
		return groups, nil
	}
	return payload.Data, nil
}

// applyProbeTokenGroups replaces the placeholder group with the site's real
// key groups and adds tracked models the catalog did not list. The probe
// itself is group-agnostic, so every group of a model shares one series.
func applyProbeTokenGroups(collection *domain.Collection, groups []probeTokenGroup) {
	if collection == nil || len(groups) == 0 {
		return
	}
	byModel := make(map[string][]string)
	for _, group := range groups {
		name := strings.TrimSpace(group.GroupName)
		if name == "" {
			continue
		}
		for _, model := range group.Models {
			modelName := strings.TrimSpace(model)
			if modelName == "" {
				continue
			}
			list := byModel[modelName]
			if !slices.Contains(list, name) {
				byModel[modelName] = append(list, name)
			}
		}
	}
	indexByName := make(map[string]int, len(collection.Models))
	for index := range collection.Models {
		indexByName[collection.Models[index].RawName] = index
	}
	for modelName, groupNames := range byModel {
		index, exists := indexByName[modelName]
		if !exists {
			index = len(collection.Models)
			indexByName[modelName] = index
			collection.Models = append(collection.Models, domain.ModelObservation{RawName: modelName})
		}
		target := &collection.Models[index]
		placeholder := len(target.Groups) == 1 && target.Groups[0].RawName == "default"
		existing := make(map[string]domain.GroupObservation, len(target.Groups))
		if !placeholder {
			for _, group := range target.Groups {
				existing[group.RawName] = group
			}
		}
		merged := make([]domain.GroupObservation, 0, len(groupNames)+len(existing))
		for _, name := range groupNames {
			if group, ok := existing[name]; ok {
				merged = append(merged, group)
				delete(existing, name)
				continue
			}
			merged = append(merged, domain.GroupObservation{RawName: name, ServiceState: domain.ServiceNoSamples})
		}
		for _, group := range target.Groups {
			if _, ok := existing[group.RawName]; ok {
				merged = append(merged, group)
			}
		}
		target.Groups = merged
	}
}

// mergeProbeBatchStatus fetches the 24h series for the given models in a
// single POST and merges them into the collection. Models without probe
// traffic keep their no-samples state: the decoder maps zero-traffic slots to
// no samples rather than the plugin's misleading success_rate=100.
func mergeProbeBatchStatus(ctx context.Context, fetcher Fetcher, collection *domain.Collection, modelNames []string, batchURL string, now time.Time) error {
	if collection == nil || len(modelNames) == 0 {
		return nil
	}
	poster, ok := fetcher.(JSONPoster)
	if !ok {
		return errors.New("fetcher does not support POST batch status")
	}
	body, _, err := poster.PostJSON(ctx, batchURL, modelNames)
	if err != nil {
		return err
	}
	var payload struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode batch status: %w", err)
	}
	byModel := make(map[string]*domain.ModelObservation, len(collection.Models))
	for index := range collection.Models {
		byModel[collection.Models[index].RawName] = &collection.Models[index]
	}
	merged := 0
	for _, rawItem := range payload.Data {
		buckets, decodeErr := decodeDetailBuckets(rawItem)
		if decodeErr != nil || len(buckets) == 0 {
			continue
		}
		var named struct {
			ModelName string `json:"model_name"`
		}
		if json.Unmarshal(rawItem, &named) != nil || named.ModelName == "" {
			continue
		}
		model, exists := byModel[named.ModelName]
		if !exists {
			continue
		}
		mergeDetailBuckets(model, buckets, now)
		model.HistoryCoverageStart = now.UTC().Add(-24 * time.Hour)
		model.HistoryCoverageEnd = now.UTC()
		merged++
	}
	if merged == 0 {
		return errors.New("batch status returned no usable models")
	}
	return nil
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
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return fmt.Errorf("apply %s config defaults: %w", adapter.Key(), err)
	}
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return err
	}
	path := config.DetailPath
	template := config.DetailPathTemplate
	if template != "" {
		path = template
	}
	statusBaseURL := strings.TrimSpace(config.StatusBaseURL)
	if statusBaseURL == "" {
		statusBaseURL = site.BaseURL
	}
	// Collect's batch merge normally covers every model already; only fetch
	// details for models still missing a fresh 24h series.
	pending := staleProbeModels(collection, modelNames, now)
	if len(pending) == 0 {
		return nil
	}
	batchPath := config.BatchPath
	if batchPath == "" {
		batchPath = adapter.defaultBatchPath
	}
	if batchPath != "" {
		if batchURL, resolveErr := resolveSiteURL(statusBaseURL, batchPath); resolveErr == nil {
			if mergeErr := mergeProbeBatchStatus(ctx, fetcher, collection, pending, batchURL, now); mergeErr == nil {
				pending = staleProbeModels(collection, modelNames, now)
				if len(pending) == 0 {
					return nil
				}
			}
		}
	}
	if path == "" {
		return nil
	}
	endpoint := ""
	if template == "" {
		var err error
		endpoint, err = resolveSiteURL(statusBaseURL, path)
		if err != nil {
			return err
		}
	}
	endpointFor := func(modelName string) (string, error) {
		if template != "" {
			return probeDetailURL(statusBaseURL, template, modelName, true)
		}
		query := url.Values{}
		query.Set("model", modelName)
		query.Set("hours", "24")
		return endpoint + "?" + query.Encode(), nil
	}
	var fallbackEndpointFor func(string) (string, error)
	if template != "" {
		fallbackEndpointFor = func(modelName string) (string, error) {
			return probeDetailURL(statusBaseURL, template, modelName, false)
		}
	}
	return collectModelDetails(ctx, fetcher, collection, pending, now, 24*time.Hour, endpointFor, fallbackEndpointFor)
}

// staleProbeModels returns the tracked models whose detail series is missing
// or older than two hours, so repeated collection runs skip refetching
// details that Collect just merged.
func staleProbeModels(collection *domain.Collection, modelNames []string, now time.Time) []string {
	staleBefore := now.Add(-2 * time.Hour)
	pending := make([]string, 0, len(modelNames))
	for _, modelName := range modelNames {
		model := probeModelByName(collection, modelName)
		if model == nil {
			continue
		}
		if model.HistoryCoverageEnd.Before(staleBefore) {
			pending = append(pending, modelName)
		}
	}
	return pending
}

func probeModelByName(collection *domain.Collection, modelName string) *domain.ModelObservation {
	if collection == nil {
		return nil
	}
	for index := range collection.Models {
		if collection.Models[index].RawName == modelName {
			return &collection.Models[index]
		}
	}
	return nil
}

// probeDetailURL renders the per-model detail URL. The model-status plugin
// keys slash-containing models (e.g. "openai/gpt-oss-120b") behind a router
// that decodes %2F back to "/" before path matching, so a single-escaped
// segment is a guaranteed 404; the double-escaped form is what the plugin
// resolves. The single-escaped variant is kept as the fallback encoding.
func probeDetailURL(statusBaseURL, template, modelName string, doubleEscaped bool) (string, error) {
	escaped := url.PathEscape(modelName)
	if doubleEscaped {
		escaped = url.PathEscape(escaped)
	}
	return resolveSiteURL(statusBaseURL, strings.ReplaceAll(template, "{model}", escaped))
}

func NewAPIProbeAdapter() ProbeAdapter {
	// defaultStatusPath stays empty: the plugin's GET /status/batch only
	// echoes a "batch" placeholder — real series come from the batch POST.
	return ProbeAdapter{adapterKey: "newapi-probe", display: "NewAPI 嵌入式探针", defaultPath: "/api/model-status/embed/config/selected", defaultDetailPath: "/api/model-status/embed/status/{model}?window=24h", defaultGroupsPath: "/api/model-status/embed/token-groups", defaultBatchPath: "/api/model-status/embed/status/batch?window=24h"}
}

func CustomProbeAdapter() ProbeAdapter {
	return ProbeAdapter{adapterKey: "custom-probe", display: "自定义状态探针", defaultPath: "/api/model-status?window=3600&recent=50", defaultStatusPath: "/api/offline-models", inlineHistory: true}
}

func ModelMarketAdapter() ProbeAdapter {
	return ProbeAdapter{adapterKey: "model-market", display: "模型市场接口", defaultPath: "/api/v1/model-market?group_by=model&sort_by=model&sort_order=asc&page=1&page_size=100&range=24h", paginate: true, historyWindow: 24 * time.Hour}
}
