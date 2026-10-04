package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"relayscope/internal/domain"
	"relayscope/internal/pricing"
	"relayscope/internal/routing"
)

type pricingSource struct {
	DecoderKey string
	BaseURL    string
	Path       string
	StatusPath string
	Optional   bool
}

func attachPricingSource(ctx context.Context, site Site, fetcher Fetcher, registry *pricing.Registry, source pricingSource, collection *domain.Collection) error {
	err := fetchAndApplyPricing(ctx, site, fetcher, registry, source, collection)
	if err != nil && source.Optional {
		return nil
	}
	return err
}

func fetchAndApplyPricing(ctx context.Context, site Site, fetcher Fetcher, registry *pricing.Registry, source pricingSource, collection *domain.Collection) error {
	baseURL := strings.TrimSpace(source.BaseURL)
	if baseURL == "" {
		baseURL = site.BaseURL
	}
	endpoint, err := resolveSiteURL(baseURL, source.Path)
	if err != nil {
		return err
	}
	body, _, err := fetcher.GetBytes(ctx, endpoint)
	if err != nil {
		return err
	}
	var statusBody []byte
	if strings.TrimSpace(source.StatusPath) != "" {
		statusEndpoint, resolveErr := resolveSiteURL(baseURL, source.StatusPath)
		if resolveErr != nil {
			return resolveErr
		}
		statusBody, _, err = fetcher.GetBytes(ctx, statusEndpoint)
		if err != nil {
			return err
		}
	}
	return decodeAndApplyPricing(registry, source.DecoderKey, body, statusBody, collection)
}

func decodeAndApplyPricing(registry *pricing.Registry, decoderKey string, body, statusBody []byte, collection *domain.Collection) error {
	return decodeAndApplyPricingBodies(registry, decoderKey, [][]byte{body}, statusBody, collection)
}

func decodeAndApplyPricingBodies(registry *pricing.Registry, decoderKey string, bodies [][]byte, statusBody []byte, collection *domain.Collection) error {
	if registry == nil {
		registry = pricing.DefaultRegistry()
	}
	merged := pricing.Catalog{}
	for index, body := range bodies {
		catalog, err := registry.Decode(decoderKey, body, statusBody)
		if err != nil {
			return fmt.Errorf("decode pricing page %d with %s: %w", index+1, decoderKey, err)
		}
		mergePricingCatalog(&merged, catalog)
	}
	applyPricingCatalog(collection, merged)
	return nil
}

func mergePricingCatalog(target *pricing.Catalog, source pricing.Catalog) {
	if target.Models == nil && len(source.Models) > 0 {
		target.Models = make(map[string]pricing.ModelPrice, len(source.Models))
	}
	for name, price := range source.Models {
		target.Models[name] = price
	}
	if target.GroupPrices == nil && len(source.GroupPrices) > 0 {
		target.GroupPrices = make(map[string]map[string]pricing.DisplayPrice, len(source.GroupPrices))
	}
	for modelName, sourceGroups := range source.GroupPrices {
		groups := target.GroupPrices[modelName]
		if groups == nil {
			groups = make(map[string]pricing.DisplayPrice, len(sourceGroups))
			target.GroupPrices[modelName] = groups
		}
		for groupName, price := range sourceGroups {
			groups[groupName] = price
		}
	}
}

func applyPricingCatalog(collection *domain.Collection, catalog pricing.Catalog) {
	applyPricingCatalogAt(collection, catalog, time.Now().UTC())
}

func applyPricingCatalogAt(collection *domain.Collection, catalog pricing.Catalog, priceReadAt time.Time) {
	if collection == nil {
		return
	}
	for modelIndex := range collection.Models {
		model := &collection.Models[modelIndex]
		modelPrice, hasModelPrice := catalog.Models[model.RawName]
		groupPrices := make(map[string]pricing.DisplayPrice)
		if hasModelPrice {
			model.Extension = pricing.ModelExtension(modelPrice)
			for name, price := range pricing.PricesForModel(modelPrice) {
				groupPrices[name] = price
			}
		}
		for name, price := range catalog.GroupPrices[model.RawName] {
			groupPrices[name] = price
		}
		if len(model.Groups) == 1 && model.Groups[0].RawName == "default" && len(groupPrices) > 0 {
			if _, hasDefaultPrice := groupPrices["default"]; !hasDefaultPrice {
				baseGroup := model.Groups[0]
				names := make([]string, 0, len(groupPrices))
				for name := range groupPrices {
					names = append(names, name)
				}
				sort.Strings(names)
				model.Groups = make([]domain.GroupObservation, 0, len(names))
				for _, name := range names {
					group := baseGroup
					group.RawName = name
					group.Buckets = append([]domain.TimeBucket(nil), baseGroup.Buckets...)
					metadata := routing.ReadExtension(baseGroup.Extension)
					availability := routing.UnknownAvailability()
					if metadata.Availability != nil {
						availability = *metadata.Availability
					}
					availability.Evidence = "copied"
					known := true
					metadata.GroupKnown, metadata.Availability = &known, &availability
					group.Extension = groupPriceExtension(groupPrices[name], metadata, priceReadAt)
					model.Groups = append(model.Groups, group)
				}
				continue
			}
		}
		for groupIndex := range model.Groups {
			if groupPrice, ok := groupPrices[model.Groups[groupIndex].RawName]; ok {
				model.Groups[groupIndex].Extension = groupPriceExtension(groupPrice, routing.ReadExtension(model.Groups[groupIndex].Extension), priceReadAt)
			}
		}
	}
}

func groupPriceExtension(price pricing.DisplayPrice, metadata routing.Extension, observedAt time.Time) json.RawMessage {
	metadata.Price = nil
	if price.RoutingPrice != nil && !observedAt.IsZero() {
		copy := *price.RoutingPrice
		at := observedAt.UnixMilli()
		copy.PriceObservedAt = &at
		metadata.Price = &copy
		known := true
		metadata.GroupKnown = &known
	}
	return routing.WriteExtension(pricing.GroupExtension(price), metadata)
}
