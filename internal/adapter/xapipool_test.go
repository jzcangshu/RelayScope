package adapter

import (
	"context"
	"testing"
	"time"

	"relaypulse/internal/domain"
)

func TestXAPIPoolSharesPublicRequestRhythmAcrossTextModels(t *testing.T) {
	now := time.Date(2026, time.August, 17, 13, 30, 0, 0, time.UTC)
	responses := map[string][]byte{
		"https://example.test/api/pool/requests/heatmap?channel=text": []byte(`{"cells":[{"start":"2026-08-17T11:00:00Z","end":"2026-08-17T12:00:00Z","ok":80,"limited":10,"error":10,"total":100},{"start":"2026-08-17T12:00:00Z","end":"2026-08-17T13:00:00Z","ok":50,"limited":0,"error":0,"total":50}]}`),
	}
	collection, err := (XAPIPoolAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 2 || len(collection.CatalogRawNames) != 2 {
		t.Fatalf("models=%+v catalog=%v", collection.Models, collection.CatalogRawNames)
	}
	for _, model := range collection.Models {
		if model.Provider != "xAI" || len(model.Groups) != 1 {
			t.Fatalf("model=%+v", model)
		}
		group := model.Groups[0]
		if group.RawName != "default" || group.ServiceState != domain.ServiceHealthy || !group.ObservedAt.Equal(time.Date(2026, time.August, 17, 13, 0, 0, 0, time.UTC)) {
			t.Fatalf("group=%+v", group)
		}
		if len(group.Buckets) != 2 || *group.Buckets[0].Metrics.RequestCount != 100 || *group.Buckets[0].Metrics.SuccessCount != 80 || *group.Buckets[0].Metrics.FailureCount != 10 || *group.Buckets[0].Metrics.EmptyCount != 10 || *group.Buckets[0].Metrics.SuccessRatio != 0.8 {
			t.Fatalf("buckets=%+v", group.Buckets)
		}
	}
}

func TestXAPIPoolUsesLatestRequestRhythmForCurrentState(t *testing.T) {
	responses := map[string][]byte{
		"https://example.test/api/pool/requests/heatmap?channel=text": []byte(`{"cells":[{"start":"2026-08-17T12:00:00Z","end":"2026-08-17T13:00:00Z","ok":0,"limited":0,"error":8,"total":8}]}`),
	}
	collection, err := (XAPIPoolAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range collection.Models {
		if got := model.Groups[0].ServiceState; got != domain.ServiceFailed {
			t.Fatalf("model %q state=%q", model.RawName, got)
		}
	}
}

func TestXAPIPoolUsesPerModelRequestHeatmap(t *testing.T) {
	now := time.Date(2026, time.August, 17, 13, 30, 0, 0, time.UTC)
	responses := map[string][]byte{
		"https://example.test/api/pool/requests/heatmap?channel=text": []byte(`{"cells":[{"start":"2026-08-17T12:00:00Z","end":"2026-08-17T13:00:00Z","ok":90,"limited":0,"error":10,"total":100}],"models":[{"model":"grok-4.5","cells":[{"start":"2026-08-17T12:00:00Z","end":"2026-08-17T13:00:00Z","ok":4,"limited":0,"error":6,"total":10}]},{"model":"grok-4.6","cells":[{"start":"2026-08-17T12:00:00Z","end":"2026-08-17T13:00:00Z","ok":8,"limited":0,"error":2,"total":10}]}]}`),
	}
	collection, err := (XAPIPoolAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	wantStates := map[string]domain.ServiceState{"grok-4.5": domain.ServiceFailed, "grok-4.6": domain.ServiceDegraded}
	for _, model := range collection.Models {
		if got := model.Groups[0].ServiceState; got != wantStates[model.RawName] {
			t.Fatalf("model %q state=%q, want %q", model.RawName, got, wantStates[model.RawName])
		}
		if len(model.Groups[0].Buckets) != 1 || *model.Groups[0].Buckets[0].Metrics.RequestCount != 10 {
			t.Fatalf("model %q buckets=%+v", model.RawName, model.Groups[0].Buckets)
		}
	}
}

func TestXAPIPoolMarksModelWithoutCurrentDataFailedWhenAllSiteFails(t *testing.T) {
	now := time.Date(2026, time.August, 17, 13, 30, 0, 0, time.UTC)
	responses := map[string][]byte{
		"https://example.test/api/pool/requests/heatmap?channel=text": []byte(`{"cells":[{"start":"2026-08-17T12:00:00Z","end":"2026-08-17T13:00:00Z","ok":0,"limited":0,"error":4,"total":4}],"models":[{"model":"grok-4.5","cells":[{"start":"2026-08-17T11:00:00Z","end":"2026-08-17T12:00:00Z","ok":3,"limited":0,"error":0,"total":3}]},{"model":"grok-4.6","cells":[]}]}`),
	}
	collection, err := (XAPIPoolAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range collection.Models {
		if got := model.Groups[0].ServiceState; got != domain.ServiceFailed {
			t.Fatalf("model %q state=%q, want failed", model.RawName, got)
		}
	}
}
