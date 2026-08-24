package adapter

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"relayscope/internal/domain"
)

func TestModelProbeCollectsHourlyHistoryAndPricing(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	first := now.Add(-24 * time.Hour).Unix()
	latest := now.Add(-time.Hour).Unix()
	responses := map[string][]byte{
		"https://example.test/api/model_probe/status": []byte(`{"success":true,"data":{"models":[{"model_name":"gemini-3.5-flash","status":"available","last_probe_at":1786878000,"window_24h":{"requests":10,"success":9,"success_rate":90},"avg_latency_ms":321,"hourly":[{"hour_start":` + itoa(first) + `,"requests":4,"success":4,"success_rate":100},{"hour_start":` + itoa(latest) + `,"requests":6,"success":5,"success_rate":83.33}]}]}}`),
		"https://example.test/api/pricing":            []byte(`{"data":[{"model_name":"gemini-3.5-flash","model_ratio":2,"completion_ratio":3,"enable_groups":["free"]}]}`),
		"https://example.test/api/status":             []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_symbol":"$"}}`),
	}
	collection, err := (ModelProbeAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 1 || collection.Models[0].RawName != "gemini-3.5-flash" || len(collection.Models[0].Groups) != 1 {
		t.Fatalf("unexpected model-probe collection: %+v", collection)
	}
	group := collection.Models[0].Groups[0]
	if group.ServiceState != domain.ServiceHealthy || len(group.Buckets) != 2 || group.Metrics.RequestCount == nil || *group.Metrics.RequestCount != 10 {
		t.Fatalf("model-probe health/history lost: %+v", group)
	}
	if !strings.Contains(string(group.Extension), `"inputPerMillion":4`) || !strings.Contains(string(group.Extension), `"groupMultiplier"`) {
		t.Fatalf("model-probe pricing missing: %s", group.Extension)
	}
	if collection.Models[0].HistoryCoverageStart.IsZero() {
		t.Fatalf("expected complete 24h probe coverage: %+v", collection.Models[0])
	}
}

func TestModelProbePreservesLoginErrors(t *testing.T) {
	_, err := (ModelProbeAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "missing response") {
		t.Fatalf("expected fetch error, got %v", err)
	}
}

func TestModelProbeEmptyReportMarksAllModelsUnavailable(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	for name, payload := range map[string]string{
		"empty models": `{"success":true,"data":{"models":[]}}`,
		"blank names":  `{"success":true,"data":{"models":[{"model_name":"  "}]}}`,
	} {
		collection, err := (ModelProbeAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{
			"https://example.test/api/model_probe/status": []byte(payload),
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

func TestModelProbeUnsuccessfulResponseStillFails(t *testing.T) {
	_, err := (ModelProbeAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/model_probe/status": []byte(`{"success":false,"message":"probe disabled"}`),
	}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "probe disabled") {
		t.Fatalf("expected unsuccessful response error, got %v", err)
	}
}

func itoa(value int64) string {
	return fmt.Sprintf("%d", value)
}
