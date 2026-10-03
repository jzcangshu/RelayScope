package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"relayscope/internal/domain"
)

func TestWelfareCollectsHourlyWindowAndIgnoresOlderChecks(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 30, 0, 0, time.UTC)
	windowStart := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	if got := welfareWindowStart(now); !got.Equal(windowStart) {
		t.Fatalf("window start = %s, want %s", got, windowStart)
	}
	responses := map[string][]byte{
		"https://example.test/api/welfare/availability": []byte(`{"success":true,"data":{"groups":[{
			"id":"default","name":"默认分组","models":[
				{"id":"grok-4.7","status":"unavailable","latency_ms":9,"checked_at":"2026-10-04T00:20:00Z","history":[
					{"checked_at":"2026-10-03T00:30:00Z","status":"unavailable","latency_ms":50,"success":0,"total":1},
					{"checked_at":"2026-10-03T01:00:00Z","status":"operational","latency_ms":1000,"success":1,"total":1},
					{"checked_at":"2026-10-04T00:20:00Z","status":"unavailable","latency_ms":3000,"success":0,"total":1},
					{"checked_at":"2026-10-04T00:40:00Z","status":"operational","latency_ms":1,"success":1,"total":1}
				]},
				{"id":"grok-4.7","status":"operational","history":[{"checked_at":"2026-10-04T00:20:00Z","status":"operational","success":1,"total":1}]},
				{"id":"grok-chat-fast","status":"operational","latency_ms":10,"checked_at":"2026-10-04T00:10:00Z","history":[
					{"checked_at":"2026-10-04T00:10:00Z","status":"unavailable","latency_ms":10,"success":9,"total":10}
				]},
				{"id":"grok-imagine-video","status":"unknown","checked_at":"2026-09-22T04:33:16Z","history":[]},
				{"id":"  ","status":"operational","history":[]}
			]},
			{"id":"","name":"会员","models":[
				{"id":"grok-imagine-image","status":"operational","checked_at":"2026-10-04T00:25:00Z","history":[]}
			]}]}}`),
	}
	collection, err := (WelfareAdapter{}).Collect(context.Background(), Site{ID: 7, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.Validate(); err != nil {
		t.Fatal(err)
	}
	if !collection.CatalogComplete || collection.MissingCatalogState != "" {
		t.Fatalf("catalog = complete:%v missing:%s", collection.CatalogComplete, collection.MissingCatalogState)
	}
	if strings.Join(collection.CatalogRawNames, ",") != "grok-4.7,grok-chat-fast,grok-imagine-video,grok-imagine-image" {
		t.Fatalf("catalog = %v", collection.CatalogRawNames)
	}
	grok := collection.Models[0]
	if len(grok.Groups) != 1 || grok.Groups[0].RawName != "default" {
		t.Fatalf("duplicate model or localized group name leaked: %+v", grok.Groups)
	}
	group := grok.Groups[0]
	if group.ServiceState != domain.ServiceDegraded || group.Metrics.RequestCount == nil || *group.Metrics.RequestCount != 2 || *group.Metrics.SuccessCount != 1 {
		t.Fatalf("24h totals should ignore the pre-window and future checks: %+v", group)
	}
	if group.Metrics.AverageLatencyMS == nil || *group.Metrics.AverageLatencyMS != 2000 {
		t.Fatalf("latency = %v, want weighted 2000", group.Metrics.AverageLatencyMS)
	}
	if len(group.Buckets) != 2 || !group.Buckets[0].Start.Equal(windowStart) || group.Buckets[0].Resolution != time.Hour || !group.Buckets[1].Start.Equal(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("hourly buckets = %+v", group.Buckets)
	}
	if grok.HistoryCoverageStart.IsZero() || !grok.HistoryCoverageStart.Equal(windowStart) || !grok.HistoryCoverageEnd.Equal(now) {
		t.Fatalf("coverage = (%s, %s), want (%s, %s)", grok.HistoryCoverageStart, grok.HistoryCoverageEnd, windowStart, now)
	}
	fast := collection.Models[1]
	if fast.Groups[0].ServiceState != domain.ServiceHealthy || *fast.Groups[0].Metrics.SuccessRatio != 0.9 {
		t.Fatalf("latest-check status must not override the 24h ratio: %+v", fast.Groups[0])
	}
	if !fast.HistoryCoverageStart.IsZero() || !fast.HistoryCoverageEnd.IsZero() {
		t.Fatalf("history that starts inside the window is capped or incomplete and must not claim coverage: %+v", fast)
	}
	video := collection.Models[2]
	if video.Groups[0].ServiceState != domain.ServiceNoSamples || len(video.Groups[0].Buckets) != 0 || !video.HistoryCoverageStart.IsZero() {
		t.Fatalf("unchecked model = %+v", video)
	}
	image := collection.Models[3]
	if image.Groups[0].RawName != "default" || image.Groups[0].ServiceState != domain.ServiceHealthy || len(image.Groups[0].Buckets) != 1 || *image.Groups[0].Metrics.RequestCount != 1 {
		t.Fatalf("empty history should keep the latest real check, group = %+v", image.Groups[0])
	}
}

func TestWelfareEmptyAndUnsuccessfulReports(t *testing.T) {
	now := time.Date(2026, 10, 4, 0, 30, 0, 0, time.UTC)
	empty, err := (WelfareAdapter{}).Collect(context.Background(), Site{ID: 7, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/welfare/availability": []byte(`{"success":true,"data":{"groups":[{"id":"default","models":[{"id":"  "}]}]}}`),
	}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Models) != 0 || empty.MissingCatalogState != domain.ServiceFailed {
		t.Fatalf("blank catalog = %+v", empty)
	}
	if err := empty.Validate(); err != nil {
		t.Fatal(err)
	}
	_, err = (WelfareAdapter{}).Collect(context.Background(), Site{ID: 7, BaseURL: "https://example.test", ConfigJSON: `{"availabilityPath":"/custom"}`}, fakeFetcher{responses: map[string][]byte{
		"https://example.test/custom": []byte(`{"success":false,"message":"probe paused"}`),
	}}, now)
	if err == nil || !strings.Contains(err.Error(), "probe paused") {
		t.Fatalf("expected unsuccessful response, got %v", err)
	}
}

func TestWelfareAnnouncementsComeFromNotices(t *testing.T) {
	notices := []byte(`{"success":true,"data":{"items":[
		{"id":1,"title":"加餐","content":"今晚不清零","level":"warning","createdAt":"2026-10-03T10:41:39.100089Z"},
		{"id":0,"title":"丢弃","content":"no id"}
	]}}`)
	anns, err := (WelfareAdapter{}).CollectAnnouncements(context.Background(), Site{ID: 7, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/welfare/notices?size=50": notices,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(anns) != 1 || anns[0].ExternalID != "1" || anns[0].Title != "加餐" || anns[0].Type != "warning" || anns[0].PublishedAt.IsZero() {
		t.Fatalf("notices = %+v", anns)
	}
	disabled, err := (WelfareAdapter{}).CollectAnnouncements(context.Background(), Site{ID: 7, BaseURL: "https://example.test", ConfigJSON: `{"announcementMode":"disabled"}`}, fakeFetcher{responses: map[string][]byte{}})
	if err != nil || disabled != nil {
		t.Fatalf("disabled announcements = %v, %v", disabled, err)
	}
}
