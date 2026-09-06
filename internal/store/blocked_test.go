package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"relayscope/internal/domain"
	"relayscope/internal/matcher"
)

// TestRemoveRawModelsClearsMatchesAndHidesRows exercises both statements of
// RemoveRawModels against a model that has rule matches and public rows, the
// shape every production blocked model has.
func TestRemoveRawModelsClearsMatchesAndHidesRows(t *testing.T) {
	ctx := context.Background()
	dbStore := openTestStore(t)
	site, err := dbStore.CreateSite(ctx, Site{Name: "blocked-site", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "newapi-pricing", Enabled: true, Interval: 15 * time.Minute})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	if err := dbStore.CreateRule(ctx, matcher.Rule{Provider: "Test", CanonicalName: "blocked-probe", RequiredTerms: []string{"claude"}, Enabled: true}); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	collected := time.Now().UTC().Truncate(time.Millisecond)
	collection := domain.Collection{
		SiteID:          site.ID,
		CollectedAt:     collected,
		ObservedAt:      collected,
		CatalogComplete: true,
		Models: []domain.ModelObservation{
			{RawName: "只会喵喵叫/claude-opus-5", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}},
			{RawName: "kept-model", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}},
		},
	}
	if _, _, err := dbStore.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatalf("apply collection: %v", err)
	}
	rules, err := dbStore.ListRules(ctx)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}
	if err := dbStore.RefreshAllMatches(ctx, engine, collected); err != nil {
		t.Fatalf("refresh matches: %v", err)
	}

	rows, err := dbStore.QueryPublicRows(ctx, "", "")
	if err != nil {
		t.Fatalf("query public rows before removal: %v", err)
	}
	visibleBefore := false
	for _, row := range rows {
		if row.RawModelName == "只会喵喵叫/claude-opus-5" {
			visibleBefore = true
		}
	}
	if !visibleBefore {
		t.Fatal("precondition failed: blocked model was not publicly visible before removal")
	}

	if err := dbStore.RemoveRawModels(ctx, site.ID, []string{"只会喵喵叫/claude-opus-5"}, time.Now().UTC()); err != nil {
		t.Fatalf("RemoveRawModels: %v", err)
	}

	rows, err = dbStore.QueryPublicRows(ctx, "", "")
	if err != nil {
		t.Fatalf("query public rows: %v", err)
	}
	for _, row := range rows {
		if row.RawModelName == "只会喵喵叫/claude-opus-5" {
			t.Fatalf("blocked model still publicly visible: %+v", row)
		}
	}
	unmatched, err := dbStore.ListUnmatchedModels(ctx, 100)
	if err != nil {
		t.Fatalf("list unmatched: %v", err)
	}
	for _, item := range unmatched {
		if item.RawModelName == "只会喵喵叫/claude-opus-5" {
			t.Fatalf("blocked model still unmatched-listed: %+v", item)
		}
	}
}
