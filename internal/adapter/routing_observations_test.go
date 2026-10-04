package adapter

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"relayscope/internal/domain"
	"relayscope/internal/pricing"
	"relayscope/internal/routing"
	"relayscope/internal/store"
)

type routingFetcher map[string][]byte

func (fetcher routingFetcher) GetJSON(_ context.Context, url string, target any) error {
	return json.Unmarshal(fetcher[url], target)
}

func TestRoutingCollectionCompletionTimeSurvivesStore(t *testing.T) {
	start := time.Date(2026, 10, 4, 4, 0, 0, 0, time.UTC)
	completed := start.Add(3 * time.Second)
	fetcher := routingFetcher{
		"https://example.test/api/pricing": []byte(`{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"completion_ratio":2,"enable_groups":["vip"],"supported_endpoint_types":["openai"]}]}`),
		"https://example.test/api/status":  []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`),
	}
	collection, err := (NewAPIAdapter{Now: func() time.Time { return completed }}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fetcher, start)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	_, err = db.DB().Exec(`INSERT INTO sites(id,name,base_url,source_url,adapter_key,interval_seconds,created_at,updated_at) VALUES (1,'fixture','https://example.test','https://example.test','newapi-pricing',900,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ApplyCollection(context.Background(), collection, nil); err != nil {
		t.Fatal(err)
	}
	envelope, err := db.QueryRoutingObservations(context.Background(), []int64{1}, completed.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	at := envelope.Observations[0].Availability.CollectedAt
	if at == nil || *at != completed.UnixMilli() {
		t.Fatalf("collection completion must not be substituted with collection start: %+v", envelope.Observations[0].Availability)
	}
}

func TestRoutingDetailRawGroupPreserved(t *testing.T) {
	buckets, err := decodeDetailBuckets([]byte(`{"data":{"groups":[{"group":" VIP ","series":[{"ts":1791086100,"end_ts":1791086400,"success_rate":20}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) < 2 || buckets[1].Group != " VIP " {
		t.Fatalf("raw group name changed: %+v", buckets)
	}
}

func TestRoutingProbeWindowKeepsSourceBoundaries(t *testing.T) {
	const start, end = int64(1791086137), int64(1791089737)
	buckets, err := decodeDetailBuckets([]byte(`{"data":{"slot_data":[{"start_time":1791086137,"end_time":1791089737,"total_requests":20,"success_count":4,"success_rate":20}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	model := domain.ModelObservation{Groups: []domain.GroupObservation{{RawName: "vip"}}}
	mergeDetailBuckets(&model, buckets, time.Unix(end, 0))
	metadata := routing.ReadExtension(model.Groups[0].Extension)
	if metadata.Availability == nil || metadata.Availability.WindowStartedAt == nil || metadata.Availability.WindowEndedAt == nil || metadata.Availability.SourceObservedAt == nil {
		t.Fatalf("missing source window: %+v", metadata)
	}
	if *metadata.Availability.WindowStartedAt != start*1000 || *metadata.Availability.WindowEndedAt != end*1000 || *metadata.Availability.SourceObservedAt != end*1000 {
		t.Fatalf("display window substituted for source evidence: %+v", *metadata.Availability)
	}
}

func TestRoutingFailedDetailCannotBecomeOKEvidence(t *testing.T) {
	_, err := decodeDetailBuckets([]byte(`{"success":false,"data":[{"group":"vip","timestamp":1791086100,"end_timestamp":1791086400,"requests":20,"success_count":4,"success_rate":20}]}`))
	if err == nil {
		t.Fatal("business-failing detail response must be rejected")
	}
}

func TestRoutingContradictoryHealthSamplesRemainUnknown(t *testing.T) {
	now := time.Date(2026, 10, 4, 4, 0, 0, 0, time.UTC)
	for _, counts := range [][3]int64{{20, 21, 0}, {20, 4, -1}, {20, 4, 21}, {20, 4, 16}} {
		requests, successes, failures := counts[0], counts[1], counts[2]
		rate := 0.1
		model := domain.ModelObservation{Groups: []domain.GroupObservation{{RawName: "vip"}}}
		mergeDetailBucketsAt(&model, []detailBucket{{Group: "vip", Timestamp: now.Add(-5 * time.Minute).Unix(), EndTimestamp: now.Unix(), Requests: &requests, Success: &successes, Failure: &failures, SuccessRate: &rate}}, now, now)
		evidence := routing.ReadExtension(model.Groups[0].Extension).Availability
		if evidence == nil || evidence.SuccessRate != nil || evidence.Status != "unknown" {
			t.Fatalf("contradictory source sample became trusted health: counts=%v, evidence=%+v", counts, evidence)
		}
	}
	requests, successes := int64(20), int64(4)
	for _, rawRate := range []float64{math.NaN(), math.Inf(1), -1, 200} {
		if routingSuccessRate(detailBucket{Requests: &requests, Success: &successes, SuccessRate: &rawRate}) != nil {
			t.Fatalf("invalid source rate hidden by otherwise valid counts: %v", rawRate)
		}
	}
}

func TestRoutingSummaryPreflightPreservesRawModelName(t *testing.T) {
	fetcher := routingFetcher{"https://example.test/api/perf-metrics/summary?hours=24": []byte(`{"success":true,"data":{"models":[{"model_name":" M "}]}}`)}
	active, ok := summaryActiveModels(context.Background(), fetcher, "https://example.test", "/api/perf-metrics/summary", 24, []string{" M "})
	if !ok || len(active) != 1 || active[0] != " M " {
		t.Fatalf("raw active model filtered by normalized summary: %v, %v", active, ok)
	}
}

func TestRoutingBusinessFailingStatusCannotReceivePriceTimestamp(t *testing.T) {
	now := time.Date(2026, 10, 4, 4, 0, 0, 0, time.UTC)
	fetcher := routingFetcher{
		"https://example.test/api/pricing": []byte(`{"group_ratio":{"vip":1},"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"completion_ratio":2,"enable_groups":["vip"],"supported_endpoint_types":["openai"]}]}`),
		"https://example.test/api/status":  []byte(`{"ok":false,"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`),
	}
	collection, err := (NewAPIAdapter{Now: func() time.Time { return now }}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fetcher, now)
	if err != nil {
		t.Fatal(err)
	}
	if routing.ReadExtension(collection.Models[0].Groups[0].Extension).Price != nil {
		t.Fatal("failed status was stamped as successful formal price evidence")
	}
}
func (fetcher routingFetcher) GetBytes(_ context.Context, url string) ([]byte, http.Header, error) {
	return fetcher[url], nil, nil
}

func TestRoutingCollectionStampsOnlySuccessfulExactPriceAndCopiedHealth(t *testing.T) {
	at := time.Date(2026, 10, 4, 4, 0, 0, 0, time.UTC)
	clockAt := at.Add(3 * time.Second)
	fetcher := routingFetcher{
		"https://example.test/api/pricing": []byte(`{"group_ratio":{"vip":2},"data":[{"model_name":"M","quota_type":0,"model_ratio":1,"completion_ratio":2,"enable_groups":["vip"],"supported_endpoint_types":["openai"]}]}`),
		"https://example.test/api/status":  []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`),
	}
	collection, err := (NewAPIAdapter{Now: func() time.Time { return clockAt }}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"availabilityMode":"presence"}`}, fetcher, at)
	if err != nil {
		t.Fatal(err)
	}
	metadata := routing.ReadExtension(collection.Models[0].Groups[0].Extension)
	if metadata.Price == nil || metadata.Price.PriceObservedAt == nil || *metadata.Price.PriceObservedAt != clockAt.UnixMilli() {
		t.Fatalf("price must carry successful collection time: %+v", metadata)
	}
	if metadata.Availability == nil || metadata.Availability.Evidence != "presence" {
		t.Fatalf("presence is not measured health: %+v", metadata)
	}
	legacy := domain.Collection{Models: []domain.ModelObservation{{RawName: "M", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceFailed}}}}}
	catalog, err := (pricing.NewAPIDecoder{}).Decode(fetcher["https://example.test/api/pricing"], fetcher["https://example.test/api/status"])
	if err != nil {
		t.Fatal(err)
	}
	applyPricingCatalog(&legacy, catalog)
	copied := routing.ReadExtension(legacy.Models[0].Groups[0].Extension)
	if copied.Availability == nil || copied.Availability.Evidence != "copied" {
		t.Fatalf("expanded default group must be marked copied: %+v", copied)
	}
}

func TestRoutingDetailHealthKeepsUnknownProtocolAndRealWindow(t *testing.T) {
	at := time.Date(2026, 10, 4, 4, 0, 0, 0, time.UTC)
	count, successes := int64(20), int64(4)
	model := domain.ModelObservation{Groups: []domain.GroupObservation{{RawName: "vip"}}}
	mergeDetailBuckets(&model, []detailBucket{{Group: "vip", Timestamp: at.Add(-5 * time.Minute).Unix(), EndTimestamp: at.Unix(), Requests: &count, Success: &successes}}, at)
	metadata := routing.ReadExtension(model.Groups[0].Extension)
	if metadata.Availability == nil || metadata.Availability.Evidence != "group-observed" || metadata.Availability.ProtocolScope.Kind != "unknown" || metadata.Availability.SourceObservedAt == nil || *metadata.Availability.SourceObservedAt != at.UnixMilli() {
		t.Fatalf("detail window evidence must remain protocol-unknown: %+v", metadata)
	}
	mergeDetailBuckets(&model, []detailBucket{{Timestamp: at.Add(-5 * time.Minute).Unix(), EndTimestamp: at.Unix(), Requests: &count, Success: &successes}}, at)
	metadata = routing.ReadExtension(model.Groups[0].Extension)
	if metadata.Availability == nil || metadata.Availability.Evidence != "copied" {
		t.Fatalf("ungrouped series copied to vip must not become group measurement: %+v", metadata)
	}
}
