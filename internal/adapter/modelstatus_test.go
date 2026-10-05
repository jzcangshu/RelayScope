package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"relayscope/internal/domain"
)

func TestModelStatusCollectsSlotsPricingAndGroups(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	// 48 slots cover a rolling 24h window ending exactly at generated_at.
	generatedAt := now.Unix()
	slotStart := func(index int) int64 { return generatedAt - int64(48-index)*1800 }
	var slots strings.Builder
	for index := 0; index < 48; index++ {
		if index > 0 {
			slots.WriteString(",")
		}
		slots.WriteString(`{"slot":` + itoa(int64(index)) + `,"start_time":` + itoa(slotStart(index)) + `,"end_time":` + itoa(slotStart(index)+1800) + `,"total_requests":10,"success_count":9,"error_count":1,"success_rate":90}`)
	}
	responses := map[string][]byte{
		"https://example.test/api/enhancements/model-status/embed/status/all": []byte(`{"success":true,"data":[` +
			`{"model_name":"claude-fable-5","group":"default","current_status":"green","status":"healthy","total_requests":283,"success_count":283,"error_count":0,"success_rate":100,"avg_use_time":3.95,"recent_avg_first_response_time":0.4,"recent_avg_output_token_speed":0.6,"last_request_at":` + itoa(generatedAt-60) + `,"slot_data":[` + slots.String() + `]},` +
			`{"model_name":"gpt-6-sol","group":"default","current_status":"yellow","total_requests":0,"success_count":0,"error_count":0,"success_rate":0,"last_request_at":0,"slot_data":[]},` +
			`{"model_name":"gpt-6-sol","group":"vip","current_status":"red","total_requests":4,"success_count":1,"error_count":3,"success_rate":25,"last_request_at":` + itoa(generatedAt-120) + `,"slot_data":[]}]}`),
		"https://example.test/api/pricing": []byte(`{"data":[{"model_name":"claude-fable-5","model_ratio":5,"completion_ratio":5,"enable_groups":["default","vip"]}]}`),
		"https://example.test/api/status":  []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`),
	}
	collection, err := (ModelStatusAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(collection.Models), collection.Models)
	}
	first := collection.Models[0]
	if first.RawName != "claude-fable-5" || len(first.Groups) != 1 {
		t.Fatalf("unexpected first model: %+v", first)
	}
	group := first.Groups[0]
	if group.RawName != "default" || group.ServiceState != domain.ServiceHealthy {
		t.Fatalf("unexpected group health: %+v", group)
	}
	if len(group.Buckets) != 48 || group.Buckets[0].Resolution != 30*time.Minute {
		t.Fatalf("expected 48 half-hour buckets, got %d", len(group.Buckets))
	}
	bucket := group.Buckets[47]
	if bucket.Metrics.RequestCount == nil || *bucket.Metrics.RequestCount != 10 || *bucket.Metrics.FailureCount != 1 || *bucket.Metrics.SuccessRatio != 0.9 {
		t.Fatalf("unexpected bucket metrics: %+v", bucket.Metrics)
	}
	if group.Metrics.AverageLatencyMS == nil || *group.Metrics.AverageLatencyMS != 3950 {
		t.Fatalf("expected avg_use_time scaled to ms: %+v", group.Metrics)
	}
	if group.Metrics.FirstTokenMS == nil || *group.Metrics.FirstTokenMS != 400 || group.Metrics.TokensPerSecond == nil || *group.Metrics.TokensPerSecond != 0.6 {
		t.Fatalf("unexpected latency/speed metrics: %+v", group.Metrics)
	}
	if group.ObservedAt.Unix() != generatedAt-60 {
		t.Fatalf("expected observed_at from last_request_at: %v", group.ObservedAt)
	}
	if !strings.Contains(string(group.Extension), `"inputPerMillion"`) {
		t.Fatalf("pricing missing: %s", group.Extension)
	}
	if first.HistoryCoverageStart.IsZero() || first.HistoryCoverageEnd.IsZero() {
		t.Fatalf("expected 24h coverage on fresh complete window: %+v", first)
	}
	// Second model carries two source groups, both preserved with their states.
	second := collection.Models[1]
	if second.RawName != "gpt-6-sol" || len(second.Groups) != 2 {
		t.Fatalf("expected two groups for gpt-6-sol: %+v", second)
	}
	if second.Groups[0].ServiceState != domain.ServiceDegraded {
		t.Fatalf("expected yellow mapped to degraded: %+v", second.Groups[0])
	}
	if second.Groups[1].RawName != "vip" || second.Groups[1].ServiceState != domain.ServiceFailed {
		t.Fatalf("expected red mapped to failed: %+v", second.Groups[1])
	}
	// Zero-traffic group falls back to ratio-derived no-samples state.
	if second.Groups[0].Metrics.RequestCount != nil {
		t.Fatalf("zero-traffic group must not report request metrics: %+v", second.Groups[0].Metrics)
	}
	if len(collection.CatalogRawNames) != 2 {
		t.Fatalf("unexpected catalog: %v", collection.CatalogRawNames)
	}
	if err := collection.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestModelStatusCoverageRequiresFreshFullWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	staleStart := now.Add(-30 * time.Hour).Unix()
	staleEnd := now.Add(-2 * time.Hour).Unix()
	responses := map[string][]byte{
		"https://example.test/api/enhancements/model-status/embed/status/all": []byte(`{"success":true,"data":[{"model_name":"claude-fable-5","group":"default","current_status":"green","last_request_at":` + itoa(staleEnd) + `,"slot_data":[{"slot":0,"start_time":` + itoa(staleStart) + `,"end_time":` + itoa(staleEnd) + `,"total_requests":1,"success_count":1,"error_count":0,"success_rate":100}]}]}`),
		"https://example.test/api/pricing":                                    []byte(`{"data":[{"model_name":"claude-fable-5","model_ratio":5,"completion_ratio":5,"enable_groups":["default"]}]}`),
		"https://example.test/api/status":                                     []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`),
	}
	collection, err := (ModelStatusAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !collection.Models[0].HistoryCoverageStart.IsZero() || !collection.Models[0].HistoryCoverageEnd.IsZero() {
		t.Fatalf("stale window must not claim coverage: %+v", collection.Models[0])
	}
	if err := collection.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestModelStatusStateFallsBackToWordStatus(t *testing.T) {
	now := time.Now()
	responses := map[string][]byte{
		"https://example.test/api/enhancements/model-status/embed/status/all": []byte(`{"success":true,"data":[{"model_name":"m","group":"default","current_status":"","status":"degraded","slot_data":[]}]}`),
		"https://example.test/api/pricing":                                    []byte(`{"data":[{"model_name":"m","model_ratio":1,"enable_groups":["default"]}]}`),
		"https://example.test/api/status":                                     []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`),
	}
	collection, err := (ModelStatusAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if collection.Models[0].Groups[0].ServiceState != domain.ServiceDegraded {
		t.Fatalf("expected wordly status fallback: %+v", collection.Models[0].Groups[0])
	}
}

func TestModelStatusEmptyReportMarksAllModelsUnavailable(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for name, payload := range map[string]string{
		"empty data":  `{"success":true,"data":[]}`,
		"blank names": `{"success":true,"data":[{"model_name":"  "}]}`,
	} {
		collection, err := (ModelStatusAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{
			"https://example.test/api/enhancements/model-status/embed/status/all": []byte(payload),
		}}, now)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(collection.Models) != 0 || !collection.CatalogComplete || collection.MissingCatalogState != domain.ServiceFailed {
			t.Fatalf("%s: expected empty catalog marked failed: %+v", name, collection)
		}
		if err := collection.Validate(); err != nil {
			t.Fatalf("%s: invalid collection: %v", name, err)
		}
	}
}

func TestModelStatusUnsuccessfulResponseStillFails(t *testing.T) {
	_, err := (ModelStatusAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/enhancements/model-status/embed/status/all": []byte(`{"success":false,"message":"embed disabled"}`),
	}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "embed disabled") {
		t.Fatalf("expected unsuccessful response error, got %v", err)
	}
}

func TestModelStatusPreservesFetchErrors(t *testing.T) {
	_, err := (ModelStatusAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "missing response") {
		t.Fatalf("expected fetch error, got %v", err)
	}
}
