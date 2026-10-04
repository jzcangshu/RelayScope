package pricing

import (
	"testing"
)

func TestRoutingNewAPIExactGroupPrices(t *testing.T) {
	body := []byte(`{"group_ratio":{"free":0.1,"vip":2},"data":[{"model_name":"Model/A","quota_type":0,"model_ratio":2,"completion_ratio":3,"enable_groups":["free","vip"],"supported_endpoint_types":["openai","openai-response"]}]}`)
	status := []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`)
	catalog, err := (NewAPIDecoder{}).Decode(body, status)
	if err != nil {
		t.Fatal(err)
	}
	prices := PricesForModel(catalog.Models["Model/A"])
	free, vip := prices["free"].RoutingPrice, prices["vip"].RoutingPrice
	if free == nil || vip == nil {
		t.Fatal("explicit group prices must carry routing evidence")
	}
	if *free.InputPerMillion != "0.4" || *free.OutputPerMillion != "1.2" || *vip.InputPerMillion != "8" {
		t.Fatalf("incorrect decimal group prices: free=%+v vip=%+v", free, vip)
	}
	if free.ProtocolScope.Kind != "explicit" || len(free.ProtocolScope.Protocols) != 2 || free.PriceObservedAt != nil {
		t.Fatalf("decoder must preserve declared protocols and leave collection time to collector: %+v", free)
	}
}

func TestRoutingNewAPIMissingEvidenceDoesNotInheritDisplayDefaults(t *testing.T) {
	cases := []struct{ name, body, status string }{
		{"missing group ratio", `{"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"completion_ratio":1,"enable_groups":["vip"]}]}`, `{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`},
		{"missing currency", `{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"completion_ratio":1,"enable_groups":["vip"]}]}`, `{"data":{"quota_per_unit":500000}}`},
		{"custom currency", `{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"completion_ratio":1,"enable_groups":["vip"]}]}`, `{"data":{"quota_per_unit":500000,"quota_display_type":"CUSTOM","custom_currency_symbol":"$"}}`},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			catalog, err := (NewAPIDecoder{}).Decode([]byte(item.body), []byte(item.status))
			if err != nil {
				t.Fatal(err)
			}
			price := PricesForModel(catalog.Models["M"])["vip"]
			if price.RoutingPrice != nil {
				t.Fatalf("missing evidence became routing price: %+v", price.RoutingPrice)
			}
		})
	}
}

func TestRoutingNewAPIPartialPriceAndUnknownBilling(t *testing.T) {
	status := []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`)
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"enable_groups":["vip"],"supported_endpoint_types":["openai"]}]}`), status)
	if err != nil {
		t.Fatal(err)
	}
	price := PricesForModel(catalog.Models["M"])["vip"].RoutingPrice
	if price == nil || price.InputPerMillion == nil || *price.InputPerMillion != "2" || price.OutputPerMillion != nil {
		t.Fatalf("missing output must preserve proven input price without default: %+v", price)
	}
	catalog, err = (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"vip":1},"data":[{"model_name":"M","model_ratio":1,"completion_ratio":1,"enable_groups":["vip"]}]}`), status)
	if err != nil {
		t.Fatal(err)
	}
	if PricesForModel(catalog.Models["M"])["vip"].RoutingPrice != nil {
		t.Fatal("missing billing kind cannot default to per-token")
	}
}

func TestRoutingNewAPIUnknownProtocolAndTrueZero(t *testing.T) {
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"model_ratio":0,"completion_ratio":0,"enable_groups":["vip"]}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`))
	if err != nil {
		t.Fatal(err)
	}
	price := PricesForModel(catalog.Models["M"])["vip"].RoutingPrice
	if price == nil || *price.InputPerMillion != "0" || *price.OutputPerMillion != "0" || price.ProtocolScope.Kind != "unknown" {
		t.Fatalf("zero price and unknown protocol must remain distinct: %+v", price)
	}
}

func TestRoutingNewAPIPreservesRawIdentityAndRejectsUnsafeAmounts(t *testing.T) {
	catalog, err := (NewAPIDecoder{}).Decode([]byte(`{"group_ratio":{" VIP ":1},"data":[{"model_name":" Model/A ","quota_type":0,"model_ratio":0.05,"completion_ratio":2,"enable_groups":[" VIP "]}]}`), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`))
	if err != nil {
		t.Fatal(err)
	}
	model, exists := catalog.Models[" Model/A "]
	if !exists || PricesForModel(model)[" VIP "].RoutingPrice == nil {
		t.Fatal("source identities were trimmed or substituted")
	}
	for _, value := range []any{"1/2", "1e999999999999", "-1", "0.1junk"} {
		if sourceAmount(value) != nil {
			t.Fatalf("unsafe decimal accepted: %v", value)
		}
	}
}

func TestRoutingNewAPIRateMatchesDeclaredDisplayUnit(t *testing.T) {
	body := []byte(`{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"completion_ratio":2,"enable_groups":["vip"]}]}`)
	catalog, err := (NewAPIDecoder{}).Decode(body, []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_exchange_rate":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	price := PricesForModel(catalog.Models["M"])["vip"].RoutingPrice
	if price == nil || *price.InputPerMillion != "4" || *price.OutputPerMillion != "8" {
		t.Fatalf("machine price lost the declared source display rate: %+v", price)
	}
}

func TestRoutingNewAPIFailedOrMalformedStatusCannotAuthorizePrice(t *testing.T) {
	body := []byte(`{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"completion_ratio":2,"enable_groups":["vip"]}]}`)
	for _, status := range []string{
		`{"success":false,"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`,
		`{"ok":false,"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`,
		`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}} {"success":false}`,
	} {
		catalog, err := (NewAPIDecoder{}).Decode(body, []byte(status))
		if err != nil {
			t.Fatal(err)
		}
		if PricesForModel(catalog.Models["M"])["vip"].RoutingPrice != nil {
			t.Fatalf("failed or malformed status produced trusted pricing: %s", status)
		}
	}
}

func TestRoutingNewAPIExplicitBillingModeCannotFallBackToRatios(t *testing.T) {
	for _, mode := range []string{"tiered_expr", "per_request", "unknown-mode"} {
		body := `{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"billing_mode":"` + mode + `","model_ratio":1,"completion_ratio":2,"enable_groups":["vip"]}]}`
		catalog, err := (NewAPIDecoder{}).Decode([]byte(body), []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`))
		if err != nil {
			t.Fatal(err)
		}
		if PricesForModel(catalog.Models["M"])["vip"].RoutingPrice != nil {
			t.Fatalf("unsupported explicit billing mode produced trusted ratio price: %s", mode)
		}
	}
}
