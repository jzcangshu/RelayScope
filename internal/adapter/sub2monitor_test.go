package adapter

import (
	"context"
	"testing"
	"time"

	"relaypulse/internal/domain"
)

func TestSub2MonitorCollectsPrimaryAndExtraModels(t *testing.T) {
	body := []byte(`{"code":0,"message":"success","data":{"items":[{"provider":"gemini","group_name":"free","primary_model":"gemini-3.7-flash","primary_status":"operational","primary_latency_ms":420,"timeline":[{"status":"operational","latency_ms":420,"checked_at":"2026-08-16T11:59:00Z"}],"extra_models":[{"model":"gemini-3.5-flash-lite","status":"degraded","latency_ms":900}]}]}}`)
	collection, err := (Sub2MonitorAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/v1/channel-monitors": body,
	}}, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 2 || !collection.CatalogComplete || len(collection.CatalogRawNames) != 2 {
		t.Fatalf("unexpected Sub2API catalog: %+v", collection)
	}
	if collection.Models[0].Groups[0].ServiceState != domain.ServiceHealthy || collection.Models[1].Groups[0].ServiceState != domain.ServiceDegraded {
		t.Fatalf("status mapping failed: %+v", collection.Models)
	}
	if collection.Models[0].Groups[0].Metrics.AverageLatencyMS == nil || *collection.Models[0].Groups[0].Metrics.AverageLatencyMS != 420 {
		t.Fatalf("latency metric missing: %+v", collection.Models[0].Groups[0].Metrics)
	}
}

func TestSub2MonitorMergesSameModelAcrossGroups(t *testing.T) {
	body := []byte(`{"code":0,"data":{"items":[{"provider":"openai","group_name":"free","primary_model":"gpt-5.5","primary_status":"operational","timeline":[]},{"provider":"openai","group_name":"vip","primary_model":"gpt-5.5","primary_status":"failed","timeline":[]}]}}`)
	collection, err := (Sub2MonitorAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/v1/channel-monitors": body,
	}}, time.Now())
	if err != nil || len(collection.Models) != 1 || len(collection.Models[0].Groups) != 2 {
		t.Fatalf("same model groups were not merged: %+v err=%v", collection, err)
	}
}
