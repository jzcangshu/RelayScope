package adapter

import (
	"context"
	"testing"
	"time"

	"relayscope/internal/domain"
)

func TestModelPulseCollectsMinuteHealthAndPricing(t *testing.T) {
	now := time.Date(2026, time.August, 22, 13, 0, 0, 0, time.UTC)
	responses := map[string][]byte{
		"https://example.test/api/model-pulse": []byte(`{"data":{"models":[{"model":"gpt-5.6","requests":10,"failures":2,"success_rate":80,"avg_latency_ms":125,"last_active_at":1787403540,"minutes":[{"ts":1787403480,"requests":4,"failures":0},{"ts":1787403540,"requests":6,"failures":2}]},{"model":"idle","requests":0,"failures":0,"minutes":[{"ts":1787403540,"requests":0,"failures":0}]}]}}`),
		"https://example.test/api/pricing":     []byte(`{"data":[{"model":"gpt-5.6","model_ratio":1,"enable_groups":["default"]}]}`),
		"https://example.test/api/status":      []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`),
	}
	collection, err := (ModelPulseAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 2 || len(collection.CatalogRawNames) != 2 {
		t.Fatalf("models = %+v, catalog = %v", collection.Models, collection.CatalogRawNames)
	}
	group := collection.Models[0].Groups[0]
	if group.RawName != "default" || group.ServiceState != domain.ServiceDegraded || group.Metrics.RequestCount == nil || *group.Metrics.RequestCount != 10 || group.Metrics.FailureCount == nil || *group.Metrics.FailureCount != 2 {
		t.Fatalf("current group = %+v", group)
	}
	if len(group.Buckets) != 2 || group.Buckets[0].Resolution != time.Minute || group.Buckets[1].Metrics.SuccessRatio == nil || *group.Buckets[1].Metrics.SuccessRatio != 4.0/6.0 {
		t.Fatalf("minute history = %+v", group.Buckets)
	}
	idle := collection.Models[1].Groups[0]
	if idle.ServiceState != domain.ServiceNoSamples || idle.Metrics.SuccessRatio != nil {
		t.Fatalf("idle group = %+v", idle)
	}
}
