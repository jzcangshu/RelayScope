package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"relayscope/internal/domain"
	"relayscope/internal/matcher"
)

// TestPreviewRuleScansAllDiscoveredModels guards the preview against a
// site-ordered LIMIT window: a target site whose rows sort after hundreds of
// filler rows must still be scanned, and the scanned total must be reported.
func TestPreviewRuleScansAllDiscoveredModels(t *testing.T) {
	dbStore := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	// createTestSite names the site "Test", which sorts before "zzz-target"
	// under SQLite's binary collation.
	filler := createTestSite(t, dbStore)
	fillerModels := make([]domain.ModelObservation, 0, 600)
	for index := 0; index < cap(fillerModels); index++ {
		fillerModels = append(fillerModels, domain.ModelObservation{
			RawName: fmt.Sprintf("filler-model-%04d", index),
			Groups:  []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}},
		})
	}
	if _, _, err := dbStore.ApplyCollection(ctx, domain.Collection{SiteID: filler.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: fillerModels}, strings.ToLower); err != nil {
		t.Fatal(err)
	}

	target, err := dbStore.CreateSite(ctx, Site{
		Name: "zzz-target", BaseURL: "https://target.test", SourceURL: "https://target.test/pricing",
		AdapterKey: "test", Enabled: true, Interval: 20 * time.Minute, Jitter: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetModels := []domain.ModelObservation{{
		RawName: "claude-opus-5-5",
		Groups:  []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}},
	}}
	if _, _, err := dbStore.ApplyCollection(ctx, domain.Collection{SiteID: target.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: targetModels}, strings.ToLower); err != nil {
		t.Fatal(err)
	}

	rule := matcher.Rule{Provider: "Anthropic", CanonicalName: "claude-opus-5-5", RequiredTerms: []string{"opus"}, AnyTerms: []string{"5-5"}, Enabled: true}
	matches, scanned, err := dbStore.PreviewRule(ctx, rule, 500)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 601 {
		t.Fatalf("scanned = %d, want 601", scanned)
	}
	if len(matches) != 1 || matches[0].SiteName != "zzz-target" || matches[0].RawModelName != "claude-opus-5-5" {
		t.Fatalf("preview must reach sites beyond the old LIMIT window: %+v (scanned %d)", matches, scanned)
	}
}
