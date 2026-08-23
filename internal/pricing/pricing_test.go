package pricing

import (
	"math"
	"testing"
)

func TestNewAPIDecoderNormalizesRatioAndGroupPrice(t *testing.T) {
	decoder := NewAPIDecoder{}
	catalog, err := decoder.Decode([]byte(`{"group_ratio":{"free":0.5,"vip":2},"data":[{"model_name":"gpt-5.5","quota_type":0,"model_ratio":2,"completion_ratio":3,"cache_ratio":0.1,"create_cache_ratio":1.25,"enable_groups":["free","vip"]}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_symbol":"¤","custom_currency_exchange_rate":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	model := catalog.Models["gpt-5.5"]
	prices := PricesForModel(model)
	if prices["free"].CurrencySymbol != "$" {
		t.Fatalf("USD symbol = %q, want $", prices["free"].CurrencySymbol)
	}
	if prices["free"].InputPerMillion == nil || *prices["free"].InputPerMillion != 2 {
		t.Fatalf("free input price = %+v", prices["free"])
	}
	if prices["free"].OutputPerMillion == nil || *prices["free"].OutputPerMillion != 6 {
		t.Fatalf("free output price = %+v", prices["free"])
	}
	if prices["free"].CacheReadPerMillion == nil || !closeEnough(*prices["free"].CacheReadPerMillion, 0.2) ||
		prices["free"].CacheWritePerMillion == nil || !closeEnough(*prices["free"].CacheWritePerMillion, 2.5) {
		t.Fatalf("free cache prices = %+v", prices["free"])
	}
	if prices["vip"].InputPerMillion == nil || *prices["vip"].InputPerMillion != 8 || *prices["vip"].GroupMultiplier != 2 {
		t.Fatalf("vip price = %+v", prices["vip"])
	}
}

func TestNewAPIDecoderReadsStandardCachePricesFromBillingExpression(t *testing.T) {
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"weekend":0.5},"data":[{"model_name":"gpt-5.6-terra","quota_type":0,"model_ratio":1.25,"completion_ratio":6,"enable_groups":["weekend"],"billing_mode":"tiered_expr","billing_expr":"len <= 272000 ? tier(\"standard\", p * 2.5 + c * 15 + cr * 0.25 + cc * 3.125) : tier(\"long_context\", p * 5 + c * 22.5 + cr * 0.5 + cc * 6.25)"}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_symbol":"$","custom_currency_exchange_rate":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	price := PricesForModel(catalog.Models["gpt-5.6-terra"])["weekend"]
	if price.CacheReadPerMillion == nil || !closeEnough(*price.CacheReadPerMillion, 0.125) ||
		price.CacheWritePerMillion == nil || !closeEnough(*price.CacheWritePerMillion, 1.5625) {
		t.Fatalf("tiered cache prices = %+v", price)
	}
}

func TestNewAPIDecoderNormalizesFixedPriceAndCurrency(t *testing.T) {
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"default":1.5},"data":[{"model_name":"gpt-5-nano","quota_type":1,"model_price":2,"enable_groups":["default"]}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"CNY","custom_currency_symbol":"¥","custom_currency_exchange_rate":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	price := PricesForModel(catalog.Models["gpt-5-nano"])["default"]
	if price.FixedPerRequest == nil || *price.FixedPerRequest != 21 || price.Currency != "CNY" || price.CurrencySymbol != "¥" {
		t.Fatalf("fixed price = %+v", price)
	}
}

func TestPriceFromModelExtensionUsesLowestAvailableGroup(t *testing.T) {
	first := 2.0
	second := 1.0
	model := ModelPrice{
		RawName: "model", Mode: "fixed", Currency: "USD", CurrencySymbol: "$", ModelPrice: &first,
		GroupMultipliers: map[string]float64{"standard": 1, "discount": second / first},
	}
	price := PriceFromExtensions(ModelExtension(model), nil)
	if price == nil || price.FixedPerRequest == nil || *price.FixedPerRequest != 1 || price.GroupMultiplier != nil {
		t.Fatalf("model fallback price = %+v", price)
	}
}

func TestLowestAvailableIgnoresUnavailableGroups(t *testing.T) {
	unavailable := DisplayPrice{Available: false}
	first := 2.0
	second := 1.0
	best := LowestAvailable([]*DisplayPrice{
		&unavailable,
		{Available: true, InputPerMillion: &first},
		{Available: true, InputPerMillion: &second},
	})
	if best == nil || best.InputPerMillion == nil || *best.InputPerMillion != 1 {
		t.Fatalf("best price = %+v", best)
	}
}

func TestModelMarketDecoderNormalizesChannelQuotes(t *testing.T) {
	t.Parallel()

	body := []byte(`{"data":{"items":[
		{"model":"gpt-5.6-sol","channel":{"name":"discount"},"pricing":{"billing_mode":"token","currency":"USD","input_price":0.000002,"output_price":0.000006,"cache_read_price":0.0000002},"rates":{"effective_text_multiplier":0.5}},
		{"model":"gemini-3.1-pro","channel":{"name":"fixed"},"pricing":{"billing_mode":"per_request","currency":"USD","per_request_price":2},"rates":{"channel_multiplier":1}},
		{"model":"glm-5.2","channel":{"name":"unpriced"},"pricing":{"billing_mode":"token","currency":"USD"},"rates":{"channel_multiplier":1}}
	]}}`)
	catalog, err := (ModelMarketDecoder{}).Decode(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := catalog.GroupPrices["gpt-5.6-sol"]["discount"]
	if !token.Available || token.InputPerMillion == nil || !closeEnough(*token.InputPerMillion, 1) || token.OutputPerMillion == nil || !closeEnough(*token.OutputPerMillion, 3) || token.CacheReadPerMillion == nil || !closeEnough(*token.CacheReadPerMillion, 0.1) || token.GroupMultiplier == nil || !closeEnough(*token.GroupMultiplier, 0.5) {
		t.Fatalf("token quote = %+v", token)
	}
	fixed := catalog.GroupPrices["gemini-3.1-pro"]["fixed"]
	if !fixed.Available || fixed.FixedPerRequest == nil || *fixed.FixedPerRequest != 2 {
		t.Fatalf("fixed quote = %+v", fixed)
	}
	if catalog.GroupPrices["glm-5.2"]["unpriced"].Available {
		t.Fatalf("unconfigured quote unexpectedly available: %+v", catalog.GroupPrices["glm-5.2"]["unpriced"])
	}
}

func TestRegistryAllowsFuturePricingDecoders(t *testing.T) {
	decoder := stubDecoder{}
	registry, err := NewRegistry(decoder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Decode("stub", []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
}

type stubDecoder struct{}

func (stubDecoder) Key() string { return "stub" }
func (stubDecoder) Decode([]byte, []byte) (Catalog, error) {
	return Catalog{Models: map[string]ModelPrice{}}, nil
}

func closeEnough(left, right float64) bool {
	return math.Abs(left-right) < 1e-9
}
