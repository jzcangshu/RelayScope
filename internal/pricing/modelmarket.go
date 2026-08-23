package pricing

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ModelMarketDecoder reads channel-specific quotes from Sub2API-compatible
// model-market payloads. Source prices are per token and are normalized to the
// dashboard's per-million convention.
type ModelMarketDecoder struct{}

func (ModelMarketDecoder) Key() string { return "model-market" }

func (ModelMarketDecoder) Decode(pricingBody, _ []byte) (Catalog, error) {
	var value any
	if err := json.Unmarshal(pricingBody, &value); err != nil {
		return Catalog{}, fmt.Errorf("decode model-market pricing: %w", err)
	}
	items := findArray(value, 0)
	if len(items) == 0 {
		return Catalog{}, fmt.Errorf("model-market pricing contained no items")
	}
	catalog := Catalog{GroupPrices: make(map[string]map[string]DisplayPrice)}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		modelName := firstString(item, "model", "model_name", "name")
		channel, _ := item["channel"].(map[string]any)
		groupName := firstString(channel, "name", "group", "slug")
		priceObject, _ := item["pricing"].(map[string]any)
		if modelName == "" || groupName == "" || priceObject == nil {
			continue
		}
		multiplier := 1.0
		if rates, ok := item["rates"].(map[string]any); ok {
			if value := numberPointer(rates, "effective_text_multiplier", "channel_multiplier"); value != nil && *value >= 0 {
				multiplier = *value
			}
		}
		currency := strings.ToUpper(firstString(priceObject, "currency"))
		if currency == "" {
			currency = "USD"
		}
		quote := DisplayPrice{
			Mode:            "ratio",
			Currency:        currency,
			CurrencySymbol:  currencySymbol(currency),
			GroupMultiplier: floatPointer(multiplier),
		}
		mode := strings.ToLower(firstString(priceObject, "billing_mode", "mode"))
		if mode == "per_request" || mode == "request" || mode == "fixed" {
			quote.Mode = "fixed"
			quote.FixedPerRequest = scaledPrice(numberPointer(priceObject, "per_request_price", "fixed_price"), multiplier)
			quote.Available = quote.FixedPerRequest != nil
		} else {
			quote.InputPerMillion = scaledPerMillion(numberPointer(priceObject, "input_price"), multiplier)
			quote.OutputPerMillion = scaledPerMillion(numberPointer(priceObject, "output_price"), multiplier)
			quote.CacheReadPerMillion = scaledPerMillion(numberPointer(priceObject, "cache_read_price"), multiplier)
			quote.CacheWritePerMillion = scaledPerMillion(numberPointer(priceObject, "cache_write_price"), multiplier)
			quote.Available = quote.InputPerMillion != nil || quote.OutputPerMillion != nil
		}
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
	if len(catalog.GroupPrices) == 0 {
		return Catalog{}, fmt.Errorf("model-market pricing contained no valid channel quotes")
	}
	return catalog, nil
}

func scaledPerMillion(value *float64, multiplier float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value * 1_000_000 * multiplier
	return &result
}

func scaledPrice(value *float64, multiplier float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value * multiplier
	return &result
}

func currencySymbol(currency string) string {
	switch strings.ToUpper(currency) {
	case "USD":
		return "$"
	case "CNY", "RMB":
		return "¥"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	default:
		return currency + " "
	}
}
