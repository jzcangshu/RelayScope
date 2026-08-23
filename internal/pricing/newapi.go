package pricing

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var expressionCoefficientPatterns = map[string][]*regexp.Regexp{
	"cr": {
		regexp.MustCompile(`(?i)\bcr\s*\*\s*([0-9]+(?:\.[0-9]+)?)`),
		regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*\*\s*cr\b`),
	},
	"cc": {
		regexp.MustCompile(`(?i)\bcc\s*\*\s*([0-9]+(?:\.[0-9]+)?)`),
		regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*\*\s*cc\b`),
	},
}

type NewAPIDecoder struct{}

func (NewAPIDecoder) Key() string { return "newapi" }

func (NewAPIDecoder) Decode(pricingBody, statusBody []byte) (Catalog, error) {
	var value any
	if err := json.Unmarshal(pricingBody, &value); err != nil {
		return Catalog{}, fmt.Errorf("decode NewAPI pricing: %w", err)
	}
	status := decodeStatus(statusBody)
	groupRatios := decodeGroupRatios(value)
	items := findArray(value, 0)
	models := make(map[string]ModelPrice, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := firstString(object, "model_name", "model", "name", "id")
		if name == "" {
			continue
		}
		quotaType := intValue(object, "quota_type", "quotaType")
		mode := "ratio"
		if quotaType == 1 {
			mode = "fixed"
		}
		model := ModelPrice{
			RawName:          name,
			Mode:             mode,
			Currency:         status.Currency,
			CurrencySymbol:   status.CurrencySymbol,
			ModelRatio:       numberPointer(object, "model_ratio", "modelRatio"),
			ModelPrice:       numberPointer(object, "model_price", "modelPrice"),
			CompletionRatio:  numberPointer(object, "completion_ratio", "completionRatio"),
			CacheRatio:       numberPointer(object, "cache_ratio", "cacheRatio"),
			CacheCreateRatio: numberPointer(object, "create_cache_ratio", "createCacheRatio", "cache_creation_ratio", "cacheCreationRatio"),
			QuotaPerUnit:     status.QuotaPerUnit,
			GroupMultipliers: make(map[string]float64),
			ExchangeRate:     status.ExchangeRate,
		}
		billingExpression := firstString(object, "billing_expr", "billingExpr")
		if model.CacheRatio == nil {
			model.CacheReadPrice = expressionCoefficient(billingExpression, "cr")
		}
		if model.CacheCreateRatio == nil {
			model.CacheWritePrice = expressionCoefficient(billingExpression, "cc")
		}
		groups := stringSlice(object, "enable_groups", "groups", "group_names")
		if len(groups) == 0 {
			groups = []string{"default"}
		}
		for _, group := range groups {
			multiplier := 1.0
			if value, ok := groupRatios[group]; ok {
				multiplier = value
			}
			model.GroupMultipliers[group] = multiplier
		}
		models[name] = model
	}
	return Catalog{Models: models}, nil
}

type statusInfo struct {
	Currency       string
	CurrencySymbol string
	QuotaPerUnit   *float64
	ExchangeRate   float64
}

func decodeStatus(body []byte) statusInfo {
	status := statusInfo{Currency: "USD", CurrencySymbol: "$", ExchangeRate: 1}
	if len(body) == 0 {
		return status
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return status
	}
	object := findObject(value, "data", 0)
	if object == nil {
		return status
	}
	if currency := firstString(object, "quota_display_type", "currency", "currency_code"); currency != "" {
		status.Currency = currency
	}
	if symbol := firstString(object, "custom_currency_symbol", "currency_symbol"); symbol != "" {
		status.CurrencySymbol = symbol
	}
	if value := numberPointer(object, "quota_per_unit", "quotaPerUnit"); value != nil && *value > 0 {
		status.QuotaPerUnit = value
	}
	if value := numberPointer(object, "custom_currency_exchange_rate", "exchange_rate"); value != nil && *value > 0 {
		status.ExchangeRate = *value
	}
	if strings.EqualFold(status.Currency, "USD") {
		status.CurrencySymbol = "$"
	}
	return status
}

func decodeGroupRatios(value any) map[string]float64 {
	object := findObject(value, "", 0)
	if object == nil {
		return nil
	}
	raw, ok := object["group_ratio"].(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]float64, len(raw))
	for name, value := range raw {
		if number, ok := asFloat(value); ok && number >= 0 {
			result[name] = number
		}
	}
	return result
}

func displayPrice(model ModelPrice, group string) DisplayPrice {
	multiplier := 1.0
	if value, ok := model.GroupMultipliers[group]; ok {
		multiplier = value
	}
	price := DisplayPrice{
		Mode:            model.Mode,
		Currency:        model.Currency,
		CurrencySymbol:  model.CurrencySymbol,
		GroupMultiplier: floatPointer(multiplier),
	}
	if model.Mode == "fixed" {
		if model.ModelPrice != nil {
			value := *model.ModelPrice * multiplier * modelExchangeRate(model)
			price.FixedPerRequest = &value
			price.Available = true
		}
		return price
	}
	if model.ModelRatio == nil || model.QuotaPerUnit == nil || *model.QuotaPerUnit <= 0 {
		return price
	}
	input := *model.ModelRatio * 1_000_000 / *model.QuotaPerUnit * multiplier
	outputRatio := 1.0
	if model.CompletionRatio != nil && *model.CompletionRatio > 0 {
		outputRatio = *model.CompletionRatio
	}
	output := input * outputRatio
	input *= modelExchangeRate(model)
	output *= modelExchangeRate(model)
	price.InputPerMillion = &input
	price.OutputPerMillion = &output
	if model.CacheReadPrice != nil {
		value := *model.CacheReadPrice * multiplier * modelExchangeRate(model)
		price.CacheReadPerMillion = &value
	} else if model.CacheRatio != nil {
		value := input * *model.CacheRatio
		price.CacheReadPerMillion = &value
	}
	if model.CacheWritePrice != nil {
		value := *model.CacheWritePrice * multiplier * modelExchangeRate(model)
		price.CacheWritePerMillion = &value
	} else if model.CacheCreateRatio != nil {
		value := input * *model.CacheCreateRatio
		price.CacheWritePerMillion = &value
	}
	price.Available = true
	return price
}

func modelExchangeRate(model ModelPrice) float64 {
	if model.ExchangeRate > 0 {
		return model.ExchangeRate
	}
	return 1
}

func PricesForModel(model ModelPrice) map[string]DisplayPrice {
	result := make(map[string]DisplayPrice, len(model.GroupMultipliers))
	for group := range model.GroupMultipliers {
		result[group] = displayPrice(model, group)
	}
	return result
}

func findArray(value any, depth int) []any {
	if depth > 5 {
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
			if result := findArray(child, depth+1); len(result) > 0 {
				return result
			}
		}
	}
	return nil
}

func findObject(value any, key string, depth int) map[string]any {
	if depth > 5 {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if key == "" || object[key] != nil {
		if key == "" {
			return object
		}
		if nested, ok := object[key].(map[string]any); ok {
			return nested
		}
	}
	for _, child := range object {
		if nested := findObject(child, key, depth+1); nested != nil {
			return nested
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
	return ""
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
		if len(result) > 0 {
			return result
		}
	}
	return nil
}

func numberPointer(object map[string]any, keys ...string) *float64 {
	for _, key := range keys {
		if value, ok := asFloat(object[key]); ok {
			return &value
		}
	}
	return nil
}

func intValue(object map[string]any, keys ...string) int {
	for _, key := range keys {
		if value, ok := asFloat(object[key]); ok {
			return int(value)
		}
	}
	return 0
}

func asFloat(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%f", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func expressionCoefficient(expression, variable string) *float64 {
	for _, pattern := range expressionCoefficientPatterns[variable] {
		matches := pattern.FindStringSubmatch(expression)
		if len(matches) != 2 {
			continue
		}
		var value float64
		if _, err := fmt.Sscanf(matches[1], "%f", &value); err == nil {
			return &value
		}
	}
	return nil
}

func floatPointer(value float64) *float64 { return &value }
