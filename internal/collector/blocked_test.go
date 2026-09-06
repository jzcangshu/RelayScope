package collector

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"relayscope/internal/adapter"
	"relayscope/internal/matcher"
	"relayscope/internal/store"
)

func TestParseBlockedKeywords(t *testing.T) {
	tests := []struct {
		name       string
		configJSON string
		want       []string
	}{
		{name: "empty config", configJSON: `{}`, want: nil},
		{name: "invalid config", configJSON: `{blockedKeywords:[`, want: nil},
		{name: "missing key", configJSON: `{"pricingPath":"/api/pricing"}`, want: nil},
		{name: "empty entries are dropped", configJSON: `{"blockedKeywords":["", "  "]}`, want: nil},
		{name: "keywords are trimmed and lowercased", configJSON: `{"blockedKeywords":[" 只会喵喵叫 ", "MEOW-Only"]}`, want: []string{"只会喵喵叫", "meow-only"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseBlockedKeywords(test.configJSON)
			if len(got) != len(test.want) {
				t.Fatalf("parseBlockedKeywords(%q) = %q, want %q", test.configJSON, got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Fatalf("parseBlockedKeywords(%q) = %q, want %q", test.configJSON, got, test.want)
				}
			}
		})
	}
}

func TestContainsAnyKeyword(t *testing.T) {
	keywords := []string{"只会喵喵叫", "meow-only"}
	if !containsAnyKeyword("gpt-只会喵喵叫-preview", keywords) {
		t.Fatal("keyword match missed")
	}
	if !containsAnyKeyword("GPT-MEOW-ONLY", keywords) {
		t.Fatal("keyword match is case sensitive")
	}
	if containsAnyKeyword("gpt-5.5", keywords) {
		t.Fatal("clean model was marked blocked")
	}
	if containsAnyKeyword("gpt-5.5", nil) {
		t.Fatal("empty keyword list blocked a model")
	}
}

func TestCollectSiteRemovesBlockedModelsImmediately(t *testing.T) {
	t.Parallel()

	dbStore := openCollectorStore(t)
	site, err := dbStore.CreateSite(context.Background(), store.Site{Name: "pm-api", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "newapi-pricing", AdapterConfig: `{"blockedKeywords":["只会喵喵叫"]}`, Enabled: true, Interval: 20 * time.Minute})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	registry, err := adapter.NewRegistry(adapter.NewAPIAdapter{})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	// The public dashboard only shows rule-matched models, so seed a rule for
	// the healthy control model before collecting.
	if err := dbStore.CreateRule(context.Background(), matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "sol"}, Priority: 100, Enabled: true}); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	// "只会喵喵叫-hidden" is observed in a first unblocked run, then blocked in
	// the config, so the test proves the removal applies to existing models
	// instead of only hiding new ones.
	body := []byte(`{"data":[` +
		`{"model":"gpt-5.6-sol","group":"free","success_rate":0.99},` +
		`{"model":"只会喵喵叫-hidden","group":"free","success_rate":1.0}]}`)
	collector, err := New(Options{Store: dbStore, Registry: registry, Fetcher: fakeJSONFetcher{body: body}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("collector: %v", err)
	}
	if err := collector.CollectSite(context.Background(), site, time.Now().UTC()); err != nil {
		t.Fatalf("collect site before blocking: %v", err)
	}
	site.AdapterConfig = `{"blockedKeywords":["只会喵喵叫"]}`
	if err := dbStore.UpdateSite(context.Background(), site.ID, site.Name, site.AdapterKey, site.AdapterConfig, true, nil, site.Interval, site.Jitter); err != nil {
		t.Fatalf("enable blocking: %v", err)
	}
	if err := collector.CollectSite(context.Background(), site, time.Now().UTC()); err != nil {
		t.Fatalf("collect site after blocking: %v", err)
	}

	rows, err := dbStore.QueryPublicRows(context.Background(), "", "")
	if err != nil {
		t.Fatalf("query public rows: %v", err)
	}
	for _, row := range rows {
		if row.RawModelName == "只会喵喵叫-hidden" {
			t.Fatalf("blocked model still visible in public rows: %+v", row)
		}
	}
	found := false
	for _, row := range rows {
		if row.RawModelName == "gpt-5.6-sol" {
			found = true
		}
	}
	if !found {
		t.Fatalf("healthy model disappeared alongside blocked model: %+v", rows)
	}

	unmatched, err := dbStore.ListUnmatchedModels(context.Background(), 100)
	if err != nil {
		t.Fatalf("list unmatched: %v", err)
	}
	for _, item := range unmatched {
		if item.RawModelName == "只会喵喵叫-hidden" {
			t.Fatalf("blocked model still listed as unmatched: %+v", item)
		}
	}
}
