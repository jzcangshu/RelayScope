package adapter

import (
	"context"
	"strconv"
	"testing"
	"time"

	"relayscope/internal/domain"
)

func TestAIAPIProbePreservesChannelGroupsAnd24HourTimeline(t *testing.T) {
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	first := now.Add(-2 * time.Hour).Unix()
	second := now.Add(-time.Hour).Unix()
	responses := map[string][]byte{
		"https://example.test/jk/api/status?board=hot&period=24h": []byte(`{"groups":[{"provider":"gpt-5.6","channel":"vip","layers":[{"model":"gpt-5.6","current_status":{"status":1,"latency":123,"timestamp":1},"timeline":[{"timestamp":` + strconv.FormatInt(first, 10) + `,"status":1,"latency":120,"availability":100},{"timestamp":` + strconv.FormatInt(second, 10) + `,"status":0,"latency":456,"availability":0}]}]}]}`),
	}
	collection, err := (AIAPIAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"statusPath":"/jk/api/status","period":"24h","board":"hot"}`}, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 1 || len(collection.CatalogRawNames) != 1 {
		t.Fatalf("AIAPI models = %+v catalog=%v", collection.Models, collection.CatalogRawNames)
	}
	model := collection.Models[0]
	if model.RawName != "gpt-5.6" || len(model.Groups) != 1 || model.Groups[0].RawName != "vip" {
		t.Fatalf("AIAPI model/group mapping = %+v", model)
	}
	group := model.Groups[0]
	if group.ServiceState != domain.ServiceFailed || len(group.Buckets) != 2 || !group.Buckets[0].Start.Equal(now.Add(-2*time.Hour)) || !group.Buckets[1].End.Equal(now) {
		t.Fatalf("AIAPI timeline = %+v", group)
	}
	if !model.HistoryCoverageStart.Equal(now.Add(-2*time.Hour)) || !model.HistoryCoverageEnd.Equal(now) {
		t.Fatalf("AIAPI coverage = (%s, %s)", model.HistoryCoverageStart, model.HistoryCoverageEnd)
	}
}
