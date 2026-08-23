package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"relaypulse/internal/domain"
	"relaypulse/internal/pricing"
)

type uptimeKumaTestFetcher map[string][]byte

func (fetcher uptimeKumaTestFetcher) GetJSON(_ context.Context, rawURL string, target any) error {
	body, ok := fetcher[rawURL]
	if !ok {
		return fmt.Errorf("unexpected URL %s", rawURL)
	}
	return json.Unmarshal(body, target)
}

func (fetcher uptimeKumaTestFetcher) GetBytes(_ context.Context, rawURL string) ([]byte, http.Header, error) {
	body, ok := fetcher[rawURL]
	if !ok {
		return nil, nil, fmt.Errorf("unexpected URL %s", rawURL)
	}
	return body, http.Header{"Content-Type": []string{"application/json"}}, nil
}

func TestUptimeKumaAdapterUsesEachMonitorTimeline(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	fetcher := uptimeKumaTestFetcher{
		"https://status.example.test/api/status-page/ai":           []byte(`{"config":{"autoRefreshInterval":300},"publicGroupList":[{"name":"OpenAI","monitorList":[{"id":1,"name":"fast-model"},{"id":2,"name":"slow-model"},{"id":3,"name":"stale-model"},{"id":4,"name":"empty-model"}]}]}`),
		"https://status.example.test/api/status-page/heartbeat/ai": []byte(`{"heartbeatList":{"1":[{"status":1,"time":"2026-08-15 11:50:00.000"},{"status":1,"time":"2026-08-15 11:55:00.000"},{"status":1,"time":"2026-08-15 11:59:00.000"}],"2":[{"status":0,"time":"2026-08-15T08:00:00Z"},{"status":1,"time":"2026-08-15T09:00:00Z"},{"status":1,"time":"2026-08-15T10:00:00Z"}],"3":[{"status":0,"time":"2026-08-01T00:00:00Z"},{"status":0,"time":"2026-08-01T04:00:00Z"}]}}`),
	}
	collection, err := (UptimeKumaAdapter{}).Collect(context.Background(), Site{
		ID: 7, BaseURL: "https://status.example.test", SourceURL: "https://status.example.test/status/ai", ConfigJSON: `{}`,
	}, fetcher, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 4 || len(collection.CatalogRawNames) != 4 {
		t.Fatalf("models=%d catalog=%v", len(collection.Models), collection.CatalogRawNames)
	}
	fastModel := uptimeKumaTestModel(t, collection, "fast-model")
	fast := fastModel.Groups[0]
	if fast.ServiceState != domain.ServiceHealthy || len(fast.Buckets) != 3 || !fast.Buckets[len(fast.Buckets)-1].End.Equal(now) {
		t.Fatalf("fast group = %+v", fast)
	}
	if !fastModel.HistoryCoverageStart.Equal(time.Date(2026, time.August, 15, 11, 50, 0, 0, time.UTC)) || !fastModel.HistoryCoverageEnd.Equal(now) {
		t.Fatalf("fast history coverage = (%s, %s)", fastModel.HistoryCoverageStart, fastModel.HistoryCoverageEnd)
	}
	slow := uptimeKumaTestModel(t, collection, "slow-model").Groups[0]
	if slow.ServiceState != domain.ServiceHealthy || len(slow.Buckets) != 3 || slow.Metrics.SuccessRatio == nil || *slow.Metrics.SuccessRatio != 0.75 {
		t.Fatalf("slow group = %+v", slow)
	}
	stale := uptimeKumaTestModel(t, collection, "stale-model").Groups[0]
	if stale.ServiceState != domain.ServiceNoSamples || len(stale.Buckets) != 0 || stale.Metrics.SuccessRatio != nil {
		t.Fatalf("stale group = %+v", stale)
	}
	emptyModel := uptimeKumaTestModel(t, collection, "empty-model")
	empty := emptyModel.Groups[0]
	if empty.ServiceState != domain.ServiceNoSamples || len(empty.Buckets) != 0 || empty.Metrics.SuccessRatio != nil {
		t.Fatalf("empty group = %+v", empty)
	}
	if !emptyModel.HistoryCoverageStart.IsZero() || !emptyModel.HistoryCoverageEnd.IsZero() {
		t.Fatalf("empty monitor declared history coverage: %+v", emptyModel)
	}
}

func TestUptimeKumaAdapterRejectsMissingStatusSlug(t *testing.T) {
	_, err := (UptimeKumaAdapter{}).Collect(context.Background(), Site{
		ID: 1, BaseURL: "https://status.example.test", SourceURL: "https://status.example.test/",
	}, uptimeKumaTestFetcher{}, time.Now())
	if err == nil {
		t.Fatal("expected missing slug error")
	}
}

func TestUptimeKumaAdapterAttachesCrossOriginPricing(t *testing.T) {
	now := time.Date(2026, time.August, 16, 0, 0, 0, 0, time.UTC)
	fetcher := uptimeKumaTestFetcher{
		"https://status.example.test/api/status-page/ai":           []byte(`{"config":{"autoRefreshInterval":300},"publicGroupList":[{"name":"OpenAI","monitorList":[{"id":1,"name":"gpt-5.6-sol"}]}]}`),
		"https://status.example.test/api/status-page/heartbeat/ai": []byte(`{"heartbeatList":{"1":[{"status":1,"time":"2026-08-15T23:59:00Z"}]}}`),
		"https://api.example.test/api/pricing":                     []byte(`{"group_ratio":{"default":1},"data":[{"model_name":"gpt-5.6-sol","quota_type":0,"model_ratio":2,"completion_ratio":3,"enable_groups":["default"]}]}`),
		"https://api.example.test/api/status":                      []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`),
	}
	collection, err := (UptimeKumaAdapter{}).Collect(context.Background(), Site{
		ID: 7, BaseURL: "https://api.example.test", SourceURL: "https://status.example.test/status/ai",
		ConfigJSON: `{"statusBaseUrl":"https://status.example.test","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`,
	}, fetcher, now)
	if err != nil {
		t.Fatal(err)
	}
	model := uptimeKumaTestModel(t, collection, "gpt-5.6-sol")
	price := pricing.PriceFromExtensions(model.Extension, nil)
	if price == nil || price.InputPerMillion == nil || *price.InputPerMillion != 4 {
		t.Fatalf("cross-origin model price = %+v", price)
	}
}

func TestUptimeKumaAdapterKeepsHealthWhenOptionalPricingFails(t *testing.T) {
	now := time.Date(2026, time.August, 16, 0, 0, 0, 0, time.UTC)
	fetcher := uptimeKumaTestFetcher{
		"https://status.example.test/api/status-page/ai":           []byte(`{"publicGroupList":[{"name":"OpenAI","monitorList":[{"id":1,"name":"gpt-5.6-sol"}]}]}`),
		"https://status.example.test/api/status-page/heartbeat/ai": []byte(`{"heartbeatList":{"1":[{"status":1,"time":"2026-08-15T23:59:00Z"}]}}`),
	}
	collection, err := (UptimeKumaAdapter{}).Collect(context.Background(), Site{
		ID: 7, BaseURL: "https://api.example.test", SourceURL: "https://status.example.test/status/ai",
		ConfigJSON: `{"statusBaseUrl":"https://status.example.test","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingOptional":true}`,
	}, fetcher, now)
	if err != nil {
		t.Fatal(err)
	}
	if model := uptimeKumaTestModel(t, collection, "gpt-5.6-sol"); model.Groups[0].ServiceState != domain.ServiceHealthy || len(model.Extension) != 0 {
		t.Fatalf("optional pricing changed health: %+v", model)
	}
}

func TestUptimeKumaAdapterRetriesEmptyHeartbeatWithCustomPaths(t *testing.T) {
	now := time.Date(2026, time.August, 16, 0, 0, 0, 0, time.UTC)
	fetcher := &retryUptimeKumaFetcher{}
	collection, err := (UptimeKumaAdapter{}).Collect(context.Background(), Site{
		ID: 7, BaseURL: "https://status.example.test", SourceURL: "https://status.example.test/status/check",
		ConfigJSON: `{"slug":"check","statusPath":"/api/status-page/{slug}","heartbeatPath":"/api/status-page/heartbeat/{slug}","retryAttempts":2,"monitorNameMode":"suffix-model"}`,
	}, fetcher, now)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.heartbeatCalls != 3 || len(collection.Models) != 1 || collection.Models[0].RawName != "gpt-5.6-sol" || len(collection.Models[0].Groups) != 2 || collection.Models[0].Groups[0].RawName != "pro" || collection.Models[0].Groups[1].RawName != "plus+free" || collection.Models[0].Groups[0].ServiceState != domain.ServiceHealthy {
		t.Fatalf("retry collection = %+v heartbeat calls=%d", collection.Models, fetcher.heartbeatCalls)
	}
}

type retryUptimeKumaFetcher struct {
	heartbeatCalls int
}

func (fetcher *retryUptimeKumaFetcher) GetJSON(_ context.Context, rawURL string, target any) error {
	switch rawURL {
	case "https://status.example.test/api/status-page/check":
		return json.Unmarshal([]byte(`{"config":{"autoRefreshInterval":300},"publicGroupList":[{"name":"GPT","monitorList":[{"id":1,"name":"pro gpt-5.6-sol"},{"id":2,"name":"plus+free gpt-5.6-sol"}]}]}`), target)
	case "https://status.example.test/api/status-page/heartbeat/check":
		fetcher.heartbeatCalls++
		if fetcher.heartbeatCalls < 3 {
			return fmt.Errorf("temporary heartbeat failure")
		}
		return json.Unmarshal([]byte(`{"heartbeatList":{"1":[{"status":1,"time":"2026-08-15T23:59:00Z"}],"2":[{"status":1,"time":"2026-08-15T23:58:00Z"}]}}`), target)
	default:
		return fmt.Errorf("unexpected URL %s", rawURL)
	}
}

func (fetcher *retryUptimeKumaFetcher) GetBytes(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	var target json.RawMessage
	if err := fetcher.GetJSON(ctx, rawURL, &target); err != nil {
		return nil, nil, err
	}
	return target, http.Header{"Content-Type": []string{"application/json"}}, nil
}

func uptimeKumaTestModel(t *testing.T, collection domain.Collection, name string) domain.ModelObservation {
	t.Helper()
	for _, model := range collection.Models {
		if model.RawName == name {
			return model
		}
	}
	t.Fatalf("model %q not found", name)
	return domain.ModelObservation{}
}
