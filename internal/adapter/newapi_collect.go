package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"relayscope/internal/adapter/adapterutil"
	"relayscope/internal/domain"
	"relayscope/internal/pricing"
	"strings"
	"time"
)

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
