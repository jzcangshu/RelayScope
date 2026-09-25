package pricing

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ModelPlazaDecoder reads group-specific quotes from Sub2API-compatible
// model-plaza payloads. Each group lists models with a raw "pricing" object
// (the same value the site's 官方价格 column shows) plus a display-only
// "official_pricing" object. The paid price is the raw quote multiplied by the
// group's rate multiplier, mirroring the site's 实付价格 column; image-billed
// groups that opt out of the text rate use their independent image multiplier
// instead. Source prices are per token and are normalized to the dashboard's
// per-million convention.
type ModelPlazaDecoder struct{}

func (ModelPlazaDecoder) Key() string { return "model-plaza" }

func (ModelPlazaDecoder) Decode(pricingBody, _ []byte) (Catalog, error) {
	var value any
	if err := json.Unmarshal(pricingBody, &value); err != nil {
		return Catalog{}, fmt.Errorf("decode model-plaza pricing: %w", err)
	}
	data := findObject(value, "data", 0)
	if data == nil {
		return Catalog{}, fmt.Errorf("model-plaza pricing contained no data object")
	}
	rawGroups, _ := data["groups"].([]any)
	if len(rawGroups) == 0 {
		return Catalog{}, fmt.Errorf("model-plaza pricing contained no groups")
	}
	catalog := Catalog{GroupPrices: make(map[string]map[string]DisplayPrice)}
	for _, rawGroup := range rawGroups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		groupName := firstString(group, "name", "title")
		if groupName == "" {
			continue
		}
		rateMultiplier := plazaMultiplier(group, "rate_multiplier", "rateMultiplier")
		imageMultiplier := plazaMultiplier(group, "image_rate_multiplier", "imageRateMultiplier")
		imageRateIndependent, _ := group["image_rate_independent"].(bool)
		models, _ := group["models"].([]any)
		for _, rawModel := range models {
			model, ok := rawModel.(map[string]any)
			if !ok {
				continue
			}
			modelName := firstString(model, "name", "model")
			priceObject, _ := model["pricing"].(map[string]any)
			if modelName == "" || priceObject == nil {
				continue
			}
			quote := modelPlazaQuote(priceObject, rateMultiplier, imageMultiplier, imageRateIndependent)
			groups := catalog.GroupPrices[modelName]
			if groups == nil {
				groups = make(map[string]DisplayPrice)
				catalog.GroupPrices[modelName] = groups
			}
			if existing, exists := groups[groupName]; exists {
				best := LowestAvailable([]*DisplayPrice{&existing, &quote})
				if best != nil {
					groups[groupName] = *best
				}
				continue
			}
			groups[groupName] = quote
		}
	}
	if len(catalog.GroupPrices) == 0 {
		return Catalog{}, fmt.Errorf("model-plaza pricing contained no valid group quotes")
	}
	return catalog, nil
}

func modelPlazaQuote(priceObject map[string]any, rateMultiplier, imageMultiplier float64, imageRateIndependent bool) DisplayPrice {
	currency := strings.ToUpper(firstString(priceObject, "currency"))
	if currency == "" {
		currency = "USD"
	}
	multiplier := rateMultiplier
	quote := DisplayPrice{
		Mode:           "ratio",
		Currency:       currency,
		CurrencySymbol: currencySymbol(currency),
	}
	mode := strings.ToLower(firstString(priceObject, "billing_mode", "mode"))
	if mode == "image" {
		if imageRateIndependent {
			multiplier = imageMultiplier
		}
		quote.Mode = "fixed"
		quote.FixedPerRequest = scaledPrice(numberPointer(priceObject, "per_request_price", "fixed_price"), multiplier)
		quote.GroupMultiplier = floatPointer(multiplier)
		quote.Available = quote.FixedPerRequest != nil
		return quote
	}
	if mode == "per_request" || mode == "request" || mode == "fixed" {
		quote.Mode = "fixed"
		quote.FixedPerRequest = scaledPrice(numberPointer(priceObject, "per_request_price", "fixed_price"), multiplier)
		quote.GroupMultiplier = floatPointer(multiplier)
		quote.Available = quote.FixedPerRequest != nil
		return quote
	}
	quote.InputPerMillion = scaledPerMillion(numberPointer(priceObject, "input_price"), multiplier)
	quote.OutputPerMillion = scaledPerMillion(numberPointer(priceObject, "output_price"), multiplier)
	quote.CacheReadPerMillion = scaledPerMillion(numberPointer(priceObject, "cache_read_price"), multiplier)
	quote.CacheWritePerMillion = scaledPerMillion(numberPointer(priceObject, "cache_write_price", "cache_write_1h_price"), multiplier)
	quote.GroupMultiplier = floatPointer(multiplier)
	quote.Available = quote.InputPerMillion != nil || quote.OutputPerMillion != nil
	return quote
}

func plazaMultiplier(group map[string]any, keys ...string) float64 {
	if value := numberPointer(group, keys...); value != nil && *value >= 0 {
		return *value
	}
	return 1
}
