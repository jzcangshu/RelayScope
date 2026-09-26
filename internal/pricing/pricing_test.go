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
	if price.InputPerMillion == nil || !closeEnough(*price.InputPerMillion, 1.25) ||
		price.OutputPerMillion == nil || !closeEnough(*price.OutputPerMillion, 7.5) {
		t.Fatalf("tiered token prices = %+v", price)
	}
}

func TestNewAPIDecoderIgnoresLegacyRatioForExpressionBilling(t *testing.T) {
	// dudu公益站 ships deepseek-v4.1-flash with the unconfigured fallback
	// model_ratio 37.5; only the billing expression describes the price.
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"default":1},"data":[{"model_name":"deepseek-v4.1-flash","quota_type":0,"model_ratio":37.5,"model_price":0,"completion_ratio":1,"enable_groups":["default"],"billing_mode":"tiered_expr","billing_expr":"tier(\"base\", p * 2 + c * 8 + cr * 0.6)"}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_symbol":"¤","custom_currency_exchange_rate":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	price := PricesForModel(catalog.Models["deepseek-v4.1-flash"])["default"]
	if !price.Available {
		t.Fatalf("expression price unavailable: %+v", price)
	}
	if price.InputPerMillion == nil || !closeEnough(*price.InputPerMillion, 2) ||
		price.OutputPerMillion == nil || !closeEnough(*price.OutputPerMillion, 8) {
		t.Fatalf("expression token prices = %+v", price)
	}
	if price.CacheReadPerMillion == nil || !closeEnough(*price.CacheReadPerMillion, 0.6) {
		t.Fatalf("expression cache read price = %+v", price)
	}
}

func TestNewAPIDecoderExpressionGroupMultiplierAndConstantFactors(t *testing.T) {
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"default":1,"half":0.5},"data":[{"model_name":"gpt-5.6-sol","quota_type":0,"model_ratio":8,"completion_ratio":2.5,"enable_groups":["default","half"],"billing_mode":"tiered_expr","billing_expr":"param(\"stream\") == true ? (len <= 272000 ? tier(\"stream_0.5x_standard\", (p * 4 + c * 20 + cr * 0.4) * 0.5) : tier(\"stream_0.5x_long_context\", (p * 8 + c * 30 + cr * 0.8) * 0.5)) : (len <= 272000 ? tier(\"nonstream_0.5x_standard\", (p * 4 + c * 20 + cr * 0.4) * 0.5) : tier(\"nonstream_0.5x_long_context\", (p * 8 + c * 30 + cr * 0.8) * 0.5))"}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_exchange_rate":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	prices := PricesForModel(catalog.Models["gpt-5.6-sol"])
	if prices["default"].InputPerMillion == nil || !closeEnough(*prices["default"].InputPerMillion, 2) ||
		prices["default"].OutputPerMillion == nil || !closeEnough(*prices["default"].OutputPerMillion, 10) {
		t.Fatalf("default expression prices = %+v", prices["default"])
	}
	if prices["half"].InputPerMillion == nil || !closeEnough(*prices["half"].InputPerMillion, 1) {
		t.Fatalf("half group expression price = %+v", prices["half"])
	}
}

func TestNewAPIDecoderExpressionFixedTier(t *testing.T) {
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"default":1},"data":[{"model_name":"grok-imagine-image","quota_type":0,"model_ratio":37.5,"enable_groups":["default"],"billing_mode":"tiered_expr","billing_expr":"tier(\"request\", fixed(1))"}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_exchange_rate":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	price := PricesForModel(catalog.Models["grok-imagine-image"])["default"]
	if price.Mode != "fixed" || price.FixedPerRequest == nil || !closeEnough(*price.FixedPerRequest, 1) {
		t.Fatalf("fixed expression price = %+v", price)
	}
	if price.InputPerMillion != nil {
		t.Fatalf("fixed expression should not carry token prices: %+v", price)
	}
}

func TestNewAPIDecoderUnparseableExpressionHidesPrice(t *testing.T) {
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"default":1},"data":[{"model_name":"odd","quota_type":0,"model_ratio":37.5,"completion_ratio":1,"enable_groups":["default"],"billing_mode":"tiered_expr","billing_expr":"tier(\"base\", ceil(p / 1000) * 0.002)"}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_exchange_rate":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	price := PricesForModel(catalog.Models["odd"])["default"]
	if price.Available || price.InputPerMillion != nil || price.OutputPerMillion != nil {
		t.Fatalf("unparseable expression must not surface ratio prices: %+v", price)
	}
}

func TestParseBillingExpressionShapes(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantP   float64
		wantC   float64
		wantCR  float64
		wantCC  float64
		wantFix *float64
	}{
		{"plain base tier", `tier("base", p * 2 + c * 8 + cr * 0.6)`, 2, 8, 0.6, 0, nil},
		{"happycoding", `tier("base", p * 0.3 + c * 1.2 + cr * 0.006)`, 0.3, 1.2, 0.006, 0, nil},
		{"conditional standard tier", `len <= 272000 ? tier("standard", p * 2.5 + c * 15 + cr * 0.25 + cc * 3.125) : tier("long_context", p * 5 + c * 22.5 + cr * 0.5 + cc * 6.25)`, 2.5, 15, 0.25, 3.125, nil},
		{"first tier fallback", `len <= 200000 ? tier("0_200k", p * 2 + c * 6 + cr * 0.5) : tier("200k_plus", p * 4 + c * 12 + cr * 1)`, 2, 6, 0.5, 0, nil},
		{"probe conditional picks base", `(((((p <= 50)))) && (((((c <= 100))) && ((c > 0))))) ? (tier(" 探测", fixed(0.3))) : (tier("base", p * 4 + c * 10 + cr * 0.8))`, 4, 10, 0.8, 0, nil},
		{"runtime multipliers ignored", `(tier("base", p * 4.5 + c * 13.5 + cr * 0.15)) * (hour("UTC") >= 1 && hour("UTC") < 4 ? 2 : 1) * (hour("UTC") >= 6 && hour("UTC") < 10 ? 2 : 1)`, 4.5, 13.5, 0.15, 0, nil},
		{"multiline conditional", "len <= 272000\n\t? tier(\"0_272k\", p * 12.5 + c * 75 + cr * 1.25 + cc * 15.625)\n\t: tier(\"272k_plus\", p * 25 + c * 112.5 + cr * 2.5 + cc * 31.25)", 12.5, 75, 1.25, 15.625, nil},
		{"image variables tolerated", `tier("base", p * 5 + c * 10 + cr * 1.25 + img * 8 + img_o * 32)`, 5, 10, 1.25, 0, nil},
		{"cache write one hour tolerated", `tier("base", p * 2 + c * 6 + cr * 1 + cc * 1 + cc1h * 1)`, 2, 6, 1, 1, nil},
		{"embedding input only", `tier("base", p * 0.02)`, 0.02, 0, 0, 0, nil},
		{"constant factor inside tier", `tier("base", (p * 4 + c * 20 + cr * 0.4) * 0.5)`, 2, 10, 0.2, 0, nil},
	}
	fixed := 1.0
	cases = append(cases, struct {
		name    string
		raw     string
		wantP   float64
		wantC   float64
		wantCR  float64
		wantCC  float64
		wantFix *float64
	}{"fixed request tier", `tier("request", fixed(1))`, 0, 0, 0, 0, &fixed})
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result, ok := parseBillingExpression(testCase.raw)
			if !ok {
				t.Fatalf("parse failed for %s", testCase.raw)
			}
			if testCase.wantFix != nil {
				if result.fixed == nil || !closeEnough(*result.fixed, *testCase.wantFix) {
					t.Fatalf("fixed = %+v, want %v", result.fixed, *testCase.wantFix)
				}
				return
			}
			if result.fixed != nil {
				t.Fatalf("unexpected fixed price: %v", *result.fixed)
			}
			for name, want := range map[string]float64{"p": testCase.wantP, "c": testCase.wantC, "cr": testCase.wantCR, "cc": testCase.wantCC} {
				if !closeEnough(result.coefficients[name], want) {
					t.Fatalf("coefficient %s = %v, want %v (all: %v)", name, result.coefficients[name], want, result.coefficients)
				}
			}
		})
	}
}

func TestParseBillingExpressionRejectsNonLinearShapes(t *testing.T) {
	for _, raw := range []string{
		`tier("base", ceil(p / 1000) * 0.002)`,
		`tier("base", p * c)`,
		`tier("base", -p)`,
		`tier("base", p * 2 + 1)`,
		`tier("base", )`,
		``,
	} {
		if _, ok := parseBillingExpression(raw); ok {
			t.Fatalf("expected rejection for %q", raw)
		}
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

func TestNewAPIDecoderDerivesSymbolForPresetCurrencies(t *testing.T) {
	cases := []struct {
		name         string
		status       string
		wantCurrency string
		wantSymbol   string
	}{
		{"cny placeholder", `{"data":{"quota_per_unit":500000,"quota_display_type":"CNY","custom_currency_symbol":"¤","custom_currency_exchange_rate":1}}`, "CNY", "¥"},
		{"cny leftover default", `{"data":{"quota_per_unit":500000,"quota_display_type":"CNY","custom_currency_exchange_rate":1}}`, "CNY", "¥"},
		{"cny explicit symbol kept", `{"data":{"quota_per_unit":500000,"quota_display_type":"CNY","custom_currency_symbol":"￥","custom_currency_exchange_rate":1}}`, "CNY", "￥"},
		{"custom symbol kept", `{"data":{"quota_per_unit":500000,"quota_display_type":"CUSTOM","custom_currency_symbol":"🍰","custom_currency_exchange_rate":1}}`, "CUSTOM", "🍰"},
		{"usd placeholder forced dollar", `{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_symbol":"¤","custom_currency_exchange_rate":1}}`, "USD", "$"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"data":[{"model_name":"m","quota_type":1,"model_price":1,"enable_groups":["default"]}]}`), []byte(testCase.status))
			if err != nil {
				t.Fatal(err)
			}
			price := PricesForModel(catalog.Models["m"])["default"]
			if price.Currency != testCase.wantCurrency || price.CurrencySymbol != testCase.wantSymbol {
				t.Fatalf("currency = %q, symbol = %q, want %q / %q", price.Currency, price.CurrencySymbol, testCase.wantCurrency, testCase.wantSymbol)
			}
		})
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

func TestModelPlazaDecoderNormalizesGroupQuotes(t *testing.T) {
	t.Parallel()

	body := []byte(`{"code":0,"message":"success","data":{"groups":[
		{"name":"公益","rate_multiplier":1,"models":[
			{"name":"gpt-5.6-sol","pricing":{"billing_mode":"token","input_price":0.000002,"output_price":0.000006,"cache_write_price":0,"cache_write_1h_price":0,"cache_read_price":0.0000002}},
			{"name":"glm-5.2","pricing":{"billing_mode":"token"}}
		]},
		{"name":"稳定","rate_multiplier":4,"models":[
			{"name":"gpt-5.6-sol","pricing":{"billing_mode":"token","input_price":0.000002,"output_price":0.000006,"cache_write_price":null,"cache_write_1h_price":0.00000025,"cache_read_price":0.0000002}}
		]},
		{"name":"生图","rate_multiplier":10,"image_rate_independent":true,"image_rate_multiplier":0.5,"models":[
			{"name":"gpt-image-2","pricing":{"billing_mode":"image","per_request_price":0.05}}
		]}
	]}}`)
	catalog, err := (ModelPlazaDecoder{}).Decode(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	free := catalog.GroupPrices["gpt-5.6-sol"]["公益"]
	if !free.Available || free.InputPerMillion == nil || !closeEnough(*free.InputPerMillion, 2) || free.OutputPerMillion == nil || !closeEnough(*free.OutputPerMillion, 6) || free.CacheReadPerMillion == nil || !closeEnough(*free.CacheReadPerMillion, 0.2) || free.CacheWritePerMillion == nil || *free.CacheWritePerMillion != 0 {
		t.Fatalf("free quote = %+v", free)
	}
	if free.GroupMultiplier == nil || *free.GroupMultiplier != 1 {
		t.Fatalf("free multiplier = %+v", free.GroupMultiplier)
	}
	stable := catalog.GroupPrices["gpt-5.6-sol"]["稳定"]
	if !stable.Available || stable.InputPerMillion == nil || !closeEnough(*stable.InputPerMillion, 8) || stable.OutputPerMillion == nil || !closeEnough(*stable.OutputPerMillion, 24) {
		t.Fatalf("stable quote = %+v", stable)
	}
	if stable.CacheWritePerMillion == nil || !closeEnough(*stable.CacheWritePerMillion, 1) {
		t.Fatalf("stable cache write fallback = %+v", stable.CacheWritePerMillion)
	}
	if stable.GroupMultiplier == nil || *stable.GroupMultiplier != 4 {
		t.Fatalf("stable multiplier = %+v", stable.GroupMultiplier)
	}
	fixed := catalog.GroupPrices["gpt-image-2"]["生图"]
	if !fixed.Available || fixed.Mode != "fixed" || fixed.FixedPerRequest == nil || !closeEnough(*fixed.FixedPerRequest, 0.025) || fixed.GroupMultiplier == nil || !closeEnough(*fixed.GroupMultiplier, 0.5) {
		t.Fatalf("fixed quote = %+v", fixed)
	}
	if catalog.GroupPrices["glm-5.2"]["公益"].Available {
		t.Fatalf("unpriced quote unexpectedly available: %+v", catalog.GroupPrices["glm-5.2"]["公益"])
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
