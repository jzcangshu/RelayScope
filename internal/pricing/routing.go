package pricing

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"regexp"
	"strings"

	"relayscope/internal/routing"
)

func decodeRoutingStatus(body []byte) map[string]any {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil
	}
	root, _ := value.(map[string]any)
	if routingResponseFailed(root) {
		return nil
	}
	return findObject(value, "data", 0)
}

func routingResponseFailed(root map[string]any) bool {
	for _, key := range []string{"success", "ok"} {
		if success, exists := root[key].(bool); exists && !success {
			return true
		}
	}
	return false
}

// Retain source decimal precision instead of converting display float values
// back into machine prices. Missing operands never inherit display defaults.
var sourceNumber = regexp.MustCompile(`^\d{1,12}(?:\.\d{1,24})?(?:[eE][+-]?\d{1,2})?$`)

func sourceAmount(value any) *big.Rat {
	var text string
	switch value := value.(type) {
	case json.Number:
		text = value.String()
	case string:
		text = value
	default:
		return nil
	}
	if !sourceNumber.MatchString(text) {
		return nil
	}
	valueRat, ok := new(big.Rat).SetString(text)
	if !ok || valueRat.Sign() < 0 {
		return nil
	}
	return valueRat
}

func decimalPrice(value *big.Rat) *string {
	if value == nil || value.Sign() < 0 {
		return nil
	}
	scale := big.NewInt(1_000_000_000_000)
	scaled := new(big.Rat).Mul(value, new(big.Rat).SetInt(scale))
	if !scaled.IsInt() {
		return nil
	}
	text := value.FloatString(12)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	if text == "" {
		text = "0"
	}
	if len(strings.SplitN(text, ".", 2)[0]) > 12 {
		return nil
	}
	return &text
}

func declaredProtocols(value any) routing.ProtocolScope {
	items, ok := value.([]any)
	if !ok {
		return routing.ProtocolScope{Kind: "unknown"}
	}
	protocols := make([]string, 0, 3)
	seen := make(map[string]bool)
	for _, raw := range items {
		var protocol string
		switch raw {
		case "openai", "openai-completions", "/v1/chat/completions":
			protocol = "openai-completions"
		case "openai-response", "openai-responses", "/v1/responses":
			protocol = "openai-responses"
		case "anthropic", "anthropic-messages", "/v1/messages":
			protocol = "anthropic-messages"
		}
		if protocol != "" && !seen[protocol] {
			protocols = append(protocols, protocol)
			seen[protocol] = true
		}
	}
	if len(protocols) == 0 {
		return routing.ProtocolScope{Kind: "unknown"}
	}
	return routing.ProtocolScope{Kind: "explicit", Protocols: protocols}
}

func newAPIRoutingPrices(model, status, ratios map[string]any) map[string]routing.Price {
	mode := firstString(model, "billing_mode", "billingMode")
	if mode != "" && mode != "ratio" && mode != "token" && mode != "per-token" {
		return nil
	}
	billing := sourceAmount(model["quota_type"])
	if billing == nil {
		billing = sourceAmount(model["quotaType"])
	}
	if status == nil || firstString(model, "billing_expr", "billingExpr") != "" || billing == nil || billing.Sign() != 0 {
		return nil
	}
	unit := strings.ToUpper(firstString(status, "quota_display_type", "currency", "currency_code"))
	if unit != "USD" && unit != "CNY" && unit != "EUR" && unit != "GBP" {
		return nil
	}
	inputRatio := sourceAmount(model["model_ratio"])
	outputRatio := sourceAmount(model["completion_ratio"])
	quota := sourceAmount(status["quota_per_unit"])
	if inputRatio == nil || quota == nil || quota.Sign() <= 0 {
		return nil
	}
	rate := big.NewRat(1, 1)
	if declared, exists := status["custom_currency_exchange_rate"]; exists {
		rate = sourceAmount(declared)
		if rate == nil || rate.Sign() <= 0 {
			return nil
		}
	} else if unit != "USD" {
		return nil
	}
	groups, _ := model["enable_groups"].([]any)
	if groups == nil {
		groups, _ = model["groups"].([]any)
	}
	if groups == nil {
		groups, _ = model["group_names"].([]any)
	}
	result := make(map[string]routing.Price)
	for _, raw := range groups {
		group, ok := raw.(string)
		if !ok || strings.TrimSpace(group) == "" {
			continue
		}
		multiplier := sourceAmount(ratios[group])
		if multiplier == nil {
			continue
		}
		input := new(big.Rat).Mul(inputRatio, big.NewRat(1_000_000, 1))
		input.Quo(input, quota).Mul(input, multiplier).Mul(input, rate)
		var outputText *string
		if outputRatio != nil {
			outputText = decimalPrice(new(big.Rat).Mul(input, outputRatio))
		}
		inputText := decimalPrice(input)
		if inputText == nil {
			continue
		}
		included := true
		result[group] = routing.Price{Kind: "per-token", Source: "exact-group", MoneyUnit: &unit,
			InputPerMillion: inputText, OutputPerMillion: outputText, IncludesGroupMultiplier: &included,
			ProtocolScope: declaredProtocols(model["supported_endpoint_types"])}
	}
	return result
}
