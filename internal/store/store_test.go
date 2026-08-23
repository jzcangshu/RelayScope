package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relaypulse/internal/domain"
	"relaypulse/internal/matcher"
	"relaypulse/internal/pricing"
)

func TestRefreshMatchesSupportsConcurrentCollectors(t *testing.T) {
	dbStore := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, dbStore)
	now := time.Now().UTC().Truncate(time.Millisecond)
	models := make([]domain.ModelObservation, 0, 300)
	for index := 0; index < cap(models); index++ {
		models = append(models, domain.ModelObservation{
			RawName: fmt.Sprintf("gpt-5.6-sol-%03d", index),
			Groups:  []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}},
		})
	}
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: models}
	if _, _, err := dbStore.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	if err := dbStore.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules, err := dbStore.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 4
	start := make(chan struct{})
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errors <- dbStore.RefreshAllMatches(ctx, engine, now)
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent match refresh failed: %v", err)
		}
	}
}

func TestRefreshMatchesForRawModelsTouchesOnlyGivenModels(t *testing.T) {
	dbStore := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, dbStore)
	now := time.Now().UTC().Truncate(time.Millisecond)

	models := []domain.ModelObservation{
		{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}},
		{RawName: "glm-5", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}},
	}
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: models}
	_, affected, err := dbStore.ApplyCollection(ctx, collection, strings.ToLower)
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != len(models) {
		t.Fatalf("affected raw model count = %d, want %d", len(affected), len(models))
	}

	if err := dbStore.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := dbStore.CreateRule(ctx, matcher.Rule{Provider: "GLM", CanonicalName: "glm-5", RequiredTerms: []string{"glm", "5"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules, err := dbStore.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := dbStore.RefreshAllMatches(ctx, engine, now); err != nil {
		t.Fatal(err)
	}
	countMatches := func(rawModelID int64) int {
		t.Helper()
		var count int
		if err := dbStore.DB().QueryRow(`SELECT COUNT(*) FROM model_matches WHERE raw_model_id = ?`, rawModelID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if countMatches(affected[0]) != 1 || countMatches(affected[1]) != 1 {
		t.Fatalf("expected one match per model after full refresh")
	}

	// Corrupt both models' rows; the incremental refresh must restore only the
	// requested model and leave the other untouched.
	if _, err := dbStore.DB().Exec(`DELETE FROM model_matches WHERE raw_model_id IN (?, ?)`, affected[0], affected[1]); err != nil {
		t.Fatal(err)
	}
	if err := dbStore.RefreshMatchesForRawModels(ctx, engine, []int64{affected[0]}, now); err != nil {
		t.Fatal(err)
	}
	if countMatches(affected[0]) != 1 {
		t.Fatalf("requested model was not refreshed")
	}
	if countMatches(affected[1]) != 0 {
		t.Fatalf("non-requested model was modified by incremental refresh")
	}

	if err := dbStore.RefreshMatchesForRawModels(ctx, engine, nil, now); err != nil {
		t.Fatalf("empty id list should be a no-op, got %v", err)
	}
}

func TestOpenAppliesSchemaAndPragmas(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	var journalMode string
	if err := store.DB().QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal mode = %q, want wal", journalMode)
	}

	var version string
	if err := store.DB().QueryRow(`SELECT value FROM app_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != "3" {
		t.Fatalf("schema version = %q, want 3", version)
	}
}

func TestMigrationVersionIsRecordedAndIdempotent(t *testing.T) {
	store := openTestStore(t)
	var version string
	if err := store.DB().QueryRow(`SELECT value FROM app_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "3" {
		t.Fatalf("version = %q, want 3", version)
	}
	if err := store.migrate(context.Background()); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM model_rules`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("migration reran seed updates unexpectedly; rules=%d", count)
	}
}

func TestSiteSessionRequirementRoundTrips(t *testing.T) {
	t.Parallel()

	dbStore := openTestStore(t)
	created, err := dbStore.CreateSite(context.Background(), Site{
		Name: "private", BaseURL: "https://private.example.test", SourceURL: "https://private.example.test/pricing",
		AdapterKey: "test", Enabled: true, SessionRequired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sites, err := dbStore.ListAllSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || !sites[0].SessionRequired {
		t.Fatalf("created session requirement was not listed: %+v", sites)
	}
	required := false
	if err := dbStore.UpdateSite(context.Background(), created.ID, created.Name, created.AdapterKey, created.AdapterConfig, created.Enabled, &required, 15*time.Minute, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	sites, err = dbStore.ListAllSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sites[0].SessionRequired {
		t.Fatalf("updated session requirement = true, want false")
	}
}

func TestCreateSiteNormalizesEditableText(t *testing.T) {
	dbStore := openTestStore(t)
	created, err := dbStore.CreateSite(context.Background(), Site{
		Name: "  padded site  ", BaseURL: " https://padded.example ", SourceURL: " https://padded.example/status ",
		AdapterKey: " test ", AdapterConfig: "  {}  ", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "padded site" || created.BaseURL != "https://padded.example" || created.SourceURL != "https://padded.example/status" || created.AdapterKey != "test" || created.AdapterConfig != "{}" {
		t.Fatalf("created site was not normalized: %+v", created)
	}
}

func TestSoftDeleteHidesSiteFromActiveQueriesAndRestores(t *testing.T) {
	ctx := context.Background()
	dbStore := openTestStore(t)
	site, err := dbStore.CreateSite(ctx, Site{Name: "deletable", BaseURL: "https://delete.example", SourceURL: "https://delete.example/status", AdapterKey: "test", Enabled: true, Interval: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	deletedAt := time.Date(2026, time.August, 24, 1, 2, 3, 0, time.UTC)
	if err := dbStore.DeleteSite(ctx, site.ID, deletedAt); err != nil {
		t.Fatal(err)
	}
	if sites, err := dbStore.ListAllSites(ctx); err != nil || len(sites) != 0 {
		t.Fatalf("deleted site visible in active list: %+v %v", sites, err)
	}
	if due, err := dbStore.ListDueSites(ctx, deletedAt.Add(time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("deleted site visible to scheduler: %+v %v", due, err)
	}
	if err := dbStore.RestoreSite(ctx, site.ID, deletedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	restored, err := dbStore.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DeletedAt != nil || restored.Enabled {
		t.Fatalf("restored site state = %+v", restored)
	}
}

func TestSessionMetadataDoesNotExposeCiphertext(t *testing.T) {
	ctx := context.Background()
	dbStore := openTestStore(t)
	site, err := dbStore.CreateSite(ctx, Site{Name: "session-meta", BaseURL: "https://meta.example", SourceURL: "https://meta.example/status", AdapterKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := dbStore.SaveEncryptedSession(ctx, EncryptedSession{SiteID: site.ID, Purpose: "site-http", KeyVersion: 1, Nonce: []byte("nonce"), Ciphertext: []byte("secret-ciphertext"), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	metadata, err := dbStore.SessionMetadata(ctx, site.ID, "site-http")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.CiphertextBytes != len("secret-ciphertext") || metadata.NonceBytes != len("nonce") {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
}

func TestListUnmatchedModels(t *testing.T) {
	ctx := context.Background()
	dbStore := openTestStore(t)
	site, err := dbStore.CreateSite(ctx, Site{Name: "unmatched", BaseURL: "https://unmatched.example", SourceURL: "https://unmatched.example/status", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, _, err := dbStore.ApplyCollection(ctx, domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: []domain.ModelObservation{{RawName: "unmatched-model", Provider: "demo", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}}}}, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	items, err := dbStore.ListUnmatchedModels(ctx, 10)
	if err != nil || len(items) != 1 || items[0].RawModelName != "unmatched-model" {
		t.Fatalf("unmatched models = %+v, err=%v", items, err)
	}
}

func TestSaveEncryptedSessionsRollsBackEntireBatch(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	site := createTestSite(t, store)
	now := time.Now().UTC()
	err := store.SaveEncryptedSessions(context.Background(), []EncryptedSession{
		{
			SiteID: site.ID, Purpose: "collector", KeyVersion: 1,
			Nonce: []byte("valid-nonce"), Ciphertext: []byte("valid-ciphertext"), UpdatedAt: now,
		},
		{
			SiteID: site.ID + 1000, Purpose: "collector", KeyVersion: 1,
			Nonce: []byte("invalid-nonce"), Ciphertext: []byte("invalid-ciphertext"), UpdatedAt: now,
		},
	})
	if err == nil {
		t.Fatal("SaveEncryptedSessions succeeded with an unknown site")
	}

	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM encrypted_sessions`).Scan(&count); err != nil {
		t.Fatalf("count encrypted sessions: %v", err)
	}
	if count != 0 {
		t.Fatalf("encrypted session count = %d, want 0 after rollback", count)
	}
}

func TestListRecentCollectionRunsBySiteKeepsNewestRunsForEverySite(t *testing.T) {
	t.Parallel()

	dbStore := openTestStore(t)
	ctx := context.Background()
	first := createTestSite(t, dbStore)
	second, err := dbStore.CreateSite(ctx, Site{
		Name: "Second", BaseURL: "https://second.example.test", SourceURL: "https://second.example.test/status",
		AdapterKey: "test", Enabled: true, Interval: 20 * time.Minute, Jitter: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.August, 15, 8, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		for _, site := range []Site{first, second} {
			startedAt := base.Add(time.Duration(index) * time.Minute)
			runID, startErr := dbStore.StartCollectionRun(ctx, site.ID, "test", startedAt)
			if startErr != nil {
				t.Fatal(startErr)
			}
			if finishErr := dbStore.FinishCollectionRun(ctx, runID, "success", true, index+1, index+2, "", "", startedAt.Add(time.Second)); finishErr != nil {
				t.Fatal(finishErr)
			}
		}
	}

	runs, err := dbStore.ListRecentCollectionRunsBySite(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 {
		t.Fatalf("run count = %d, want 4", len(runs))
	}
	counts := map[int64]int{}
	for _, run := range runs {
		counts[run.SiteID]++
		if run.ModelsSeen < 2 {
			t.Fatalf("old run leaked into result: %+v", run)
		}
	}
	if counts[first.ID] != 2 || counts[second.ID] != 2 {
		t.Fatalf("runs per site = %+v, want two each", counts)
	}
}

func TestActiveFailureAnnouncementsUseCustomReasonAndDisappearAfterSuccess(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	started := time.Date(2026, time.August, 22, 8, 0, 0, 0, time.UTC)
	runID, err := store.StartCollectionRun(ctx, site.ID, "test", started)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishCollectionRun(ctx, runID, "failed", false, 0, 0, "challenge_failed", "Cloudflare challenge", started.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAcquisitionState(ctx, site.ID, domain.AcquisitionChallengeFailed, started.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListActiveFailureAnnouncements(ctx)
	if err != nil || len(items) != 1 || items[0].FailureCode != "challenge_failed" || items[0].Reason != "Cloudflare challenge" {
		t.Fatalf("announcements = %+v, err = %v", items, err)
	}
	if err := store.UpdateSiteFailureReason(ctx, site.ID, "源站正在处理"); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListActiveFailureAnnouncements(ctx)
	if err != nil || len(items) != 1 || items[0].Reason != "源站正在处理" {
		t.Fatalf("custom announcement = %+v, err = %v", items, err)
	}
	if err := store.SetAcquisitionState(ctx, site.ID, domain.AcquisitionFresh, started.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListActiveFailureAnnouncements(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("successful site still announced = %+v, err = %v", items, err)
	}
}

func TestApplyCollectionIsIdempotentAndPreservesNullMetrics(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	now := time.Date(2026, time.August, 13, 0, 0, 0, 0, time.UTC)
	successRatio := 0.944
	collection := domain.Collection{
		SiteID:          site.ID,
		ObservedAt:      now,
		CollectedAt:     now.Add(time.Minute),
		CatalogComplete: true,
		Models: []domain.ModelObservation{{
			RawName:  "prefix-gpt-5.6-sol-suffix",
			Provider: "OpenAI",
			Groups: []domain.GroupObservation{{
				RawName:      "free group",
				ServiceState: domain.ServiceHealthy,
				Metrics:      domain.Metrics{SuccessRatio: &successRatio},
				Buckets: []domain.TimeBucket{{
					Start:      now.Add(-5 * time.Minute),
					End:        now,
					Resolution: 5 * time.Minute,
					Metrics:    domain.Metrics{SuccessRatio: &successRatio},
				}},
			}},
		}},
	}

	for expectedRevision := int64(1); expectedRevision <= 2; expectedRevision++ {
		revision, _, err := store.ApplyCollection(ctx, collection, strings.ToLower)
		if err != nil {
			t.Fatalf("apply collection: %v", err)
		}
		if revision != expectedRevision {
			t.Fatalf("revision = %d, want %d", revision, expectedRevision)
		}
	}

	assertCount(t, store, "raw_models", 1)
	assertCount(t, store, "site_groups", 1)
	assertCount(t, store, "current_snapshots", 1)
	assertCount(t, store, "metric_buckets", 1)

	var requestCount *int64
	var storedRatio float64
	if err := store.DB().QueryRow(`SELECT request_count, success_ratio FROM current_snapshots`).Scan(&requestCount, &storedRatio); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if requestCount != nil || storedRatio != successRatio {
		t.Fatalf("null or ratio semantics lost: request=%v ratio=%v", requestCount, storedRatio)
	}
}

func TestCompleteCatalogMarksModelRemovedAfterThreeOmissions(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	now := time.Date(2026, time.August, 13, 0, 0, 0, 0, time.UTC)
	initial := domain.Collection{
		SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true,
		Models: []domain.ModelObservation{{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}}},
	}
	if _, _, err := store.ApplyCollection(ctx, initial, strings.ToLower); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if err := store.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}
	if err := store.RefreshAllMatches(ctx, engine, now); err != nil {
		t.Fatalf("seed matches: %v", err)
	}
	var matchCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM model_matches`).Scan(&matchCount); err != nil || matchCount != 1 {
		t.Fatalf("seed match count = %d, err=%v", matchCount, err)
	}

	partial := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now.Add(time.Minute), CatalogComplete: false}
	if _, _, err := store.ApplyCollection(ctx, partial, strings.ToLower); err != nil {
		t.Fatalf("apply partial catalog: %v", err)
	}
	assertRemovalEvidence(t, store, 0, false)

	for omission := 1; omission <= 3; omission++ {
		complete := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now.Add(time.Duration(omission+1) * time.Minute), CatalogComplete: true}
		if _, _, err := store.ApplyCollection(ctx, complete, strings.ToLower); err != nil {
			t.Fatalf("apply omission %d: %v", omission, err)
		}
	}
	assertRemovalEvidence(t, store, 3, true)
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM model_matches`).Scan(&matchCount); err != nil {
		t.Fatalf("count removed matches: %v", err)
	} else if matchCount != 0 {
		t.Fatalf("removed model retained %d matches", matchCount)
	}
}

func TestModelHistoryCoverageMergesOverlapAndResetsAfterGap(t *testing.T) {
	t.Parallel()

	dbStore := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, dbStore)
	now := time.Now().UTC().Truncate(time.Millisecond)
	apply := func(collectedAt, coverageStart, coverageEnd time.Time) {
		t.Helper()
		collection := domain.Collection{
			SiteID: site.ID, ObservedAt: collectedAt, CollectedAt: collectedAt, CatalogComplete: true,
			Models: []domain.ModelObservation{{
				RawName: "gpt-5.6-sol", HistoryCoverageStart: coverageStart, HistoryCoverageEnd: coverageEnd,
				Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceNoSamples}},
			}},
		}
		if _, _, err := dbStore.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
			t.Fatalf("apply collection: %v", err)
		}
	}
	readCoverage := func() (sql.NullInt64, sql.NullInt64) {
		t.Helper()
		var start, end sql.NullInt64
		if err := dbStore.DB().QueryRow(`SELECT history_coverage_start, history_coverage_end FROM raw_models WHERE raw_name = 'gpt-5.6-sol'`).Scan(&start, &end); err != nil {
			t.Fatalf("read coverage: %v", err)
		}
		return start, end
	}

	firstStart := now.Add(-24 * time.Hour)
	apply(now, firstStart, now)
	start, end := readCoverage()
	if !start.Valid || !end.Valid || start.Int64 != firstStart.UnixMilli() || end.Int64 != now.UnixMilli() {
		t.Fatalf("initial coverage = (%v, %v)", start, end)
	}

	overlapEnd := now.Add(time.Hour)
	apply(overlapEnd, now.Add(-23*time.Hour), overlapEnd)
	start, end = readCoverage()
	if start.Int64 != firstStart.UnixMilli() || end.Int64 != overlapEnd.UnixMilli() {
		t.Fatalf("overlapping coverage = (%v, %v)", start, end)
	}

	gapStart := now.Add(2 * time.Hour)
	gapEnd := gapStart.Add(24 * time.Hour)
	apply(gapEnd, gapStart, gapEnd)
	start, end = readCoverage()
	if start.Int64 != gapStart.UnixMilli() || end.Int64 != gapEnd.UnixMilli() {
		t.Fatalf("gapped coverage = (%v, %v)", start, end)
	}

	apply(gapEnd.Add(time.Minute), time.Time{}, time.Time{})
	start, end = readCoverage()
	if start.Valid || end.Valid {
		t.Fatalf("unmarked observation retained coverage = (%v, %v)", start, end)
	}
}

func TestPresenceCatalogMarksOmittedModelFailedWithoutRemovingPrice(t *testing.T) {
	t.Parallel()

	dbStore := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, dbStore)
	now := time.Date(2026, time.August, 16, 0, 0, 0, 0, time.UTC)
	initial := domain.Collection{
		SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true,
		MissingCatalogState: domain.ServiceFailed,
		Models: []domain.ModelObservation{
			{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy, Extension: json.RawMessage(`{"pricing":{"available":true,"currency":"USD"}}`)}}},
			{RawName: "gpt-5.6-terra", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}},
		},
	}
	if _, _, err := dbStore.ApplyCollection(ctx, initial, strings.ToLower); err != nil {
		t.Fatalf("seed presence catalog: %v", err)
	}

	next := domain.Collection{
		SiteID: site.ID, ObservedAt: now.Add(20 * time.Minute), CollectedAt: now.Add(20 * time.Minute),
		CatalogComplete: true, MissingCatalogState: domain.ServiceFailed,
		Models: []domain.ModelObservation{{RawName: "gpt-5.6-terra", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}}},
	}
	if _, _, err := dbStore.ApplyCollection(ctx, next, strings.ToLower); err != nil {
		t.Fatalf("apply presence omission: %v", err)
	}

	var state string
	var extension string
	var absentRuns int
	var removedAt *int64
	err := dbStore.DB().QueryRow(`SELECT snapshot.service_state, groups.source_extension, raw.absent_complete_runs, raw.removed_at
		FROM raw_models raw JOIN site_groups groups ON groups.raw_model_id = raw.id
		JOIN current_snapshots snapshot ON snapshot.group_id = groups.id
		WHERE raw.raw_name = 'gpt-5.6-sol'`).Scan(&state, &extension, &absentRuns, &removedAt)
	if err != nil {
		t.Fatalf("read omitted model: %v", err)
	}
	if state != string(domain.ServiceFailed) || !strings.Contains(extension, `"currency":"USD"`) || absentRuns != 0 || removedAt != nil {
		t.Fatalf("omitted model = state %q extension %q absence (%d, %v)", state, extension, absentRuns, removedAt)
	}
}

func TestCleanupRemovesOnlyExpiredHistory(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	now := time.Date(2026, time.August, 13, 0, 0, 0, 0, time.UTC)
	oldRatio, freshRatio := 0.5, 1.0
	collection := domain.Collection{
		SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true,
		Models: []domain.ModelObservation{{RawName: "model", Groups: []domain.GroupObservation{{
			RawName: "group", ServiceState: domain.ServiceHealthy,
			Buckets: []domain.TimeBucket{
				{Start: now.Add(-4 * 24 * time.Hour), End: now.Add(-4*24*time.Hour + 5*time.Minute), Resolution: 5 * time.Minute, Metrics: domain.Metrics{SuccessRatio: &oldRatio}},
				{Start: now.Add(-time.Hour), End: now.Add(-55 * time.Minute), Resolution: 5 * time.Minute, Metrics: domain.Metrics{SuccessRatio: &freshRatio}},
			},
		}}}},
	}
	if _, _, err := store.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatalf("apply collection: %v", err)
	}

	removed, err := store.Cleanup(ctx, now.Add(-3*24*time.Hour), 100)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	assertCount(t, store, "metric_buckets", 1)
	assertCount(t, store, "current_snapshots", 1)
}

func TestPublicRowsMarkOldSnapshotStale(t *testing.T) {
	store := openTestStore(t)
	site := createTestSite(t, store)
	if err := store.CreateRule(context.Background(), matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	collection := domain.Collection{SiteID: site.ID, ObservedAt: old, CollectedAt: old, CatalogComplete: true, Models: []domain.ModelObservation{{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}}}}
	if _, _, err := store.ApplyCollection(context.Background(), collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	rules, err := store.ListRules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllMatches(context.Background(), engine, old); err != nil {
		t.Fatal(err)
	}
	rows, err := store.QueryPublicRows(context.Background(), "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("query rows: %+v %v", rows, err)
	}
	if rows[0].AcquisitionState != domain.AcquisitionStale {
		t.Fatalf("old snapshot state = %q, want stale", rows[0].AcquisitionState)
	}
}

func TestPublicRowsExpireOldSourceSampleWithoutExpiringCollection(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	now := time.Now().UTC()
	sampleAt := now.Add(-3 * time.Hour)
	collection := domain.Collection{
		SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true,
		Models: []domain.ModelObservation{{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{
			RawName: "default", ServiceState: domain.ServiceHealthy, ObservedAt: sampleAt,
		}}}},
	}
	if _, _, err := store.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllMatches(ctx, engine, now); err != nil {
		t.Fatal(err)
	}

	rows, err := store.QueryPublicRows(ctx, "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("query rows: %+v %v", rows, err)
	}
	if rows[0].AcquisitionState != domain.AcquisitionFresh {
		t.Fatalf("acquisition state = %q, want fresh", rows[0].AcquisitionState)
	}
	if rows[0].ServiceState != domain.ServiceNoSamples {
		t.Fatalf("current state = %q, want no samples for old source observation", rows[0].ServiceState)
	}
	if !rows[0].ObservedAt.Equal(sampleAt.Truncate(time.Millisecond)) {
		t.Fatalf("observed at = %s, want %s", rows[0].ObservedAt, sampleAt)
	}
}

func TestPublicRowsHideOnlyMatureAuthoritativeNoSampleModels(t *testing.T) {
	tests := []struct {
		name          string
		coverageStart time.Duration
		modelAge      time.Duration
		state         domain.ServiceState
		metrics       domain.Metrics
		bucket        *domain.TimeBucket
		failedSite    bool
		wantRows      int
	}{
		{name: "mature covered empty model is hidden", coverageStart: -24 * time.Hour, modelAge: 25 * time.Hour, state: domain.ServiceNoSamples, wantRows: 0},
		{name: "young model stays visible", coverageStart: -24 * time.Hour, modelAge: time.Hour, state: domain.ServiceNoSamples, wantRows: 1},
		{name: "incomplete coverage stays visible", coverageStart: -12 * time.Hour, modelAge: 25 * time.Hour, state: domain.ServiceNoSamples, wantRows: 1},
		{name: "health only model without coverage stays visible", modelAge: 25 * time.Hour, state: domain.ServiceHealthy, wantRows: 1},
		{name: "positive aggregate count stays visible", coverageStart: -24 * time.Hour, modelAge: 25 * time.Hour, state: domain.ServiceNoSamples, metrics: domain.Metrics{RequestCount: testInt64Pointer(1)}, wantRows: 1},
		{name: "count free aggregate metric stays visible", coverageStart: -24 * time.Hour, modelAge: 25 * time.Hour, state: domain.ServiceNoSamples, metrics: domain.Metrics{SuccessRatio: testFloat64Pointer(0.9)}, wantRows: 1},
		{name: "positive bucket stays visible", coverageStart: -24 * time.Hour, modelAge: 25 * time.Hour, state: domain.ServiceHealthy, bucket: &domain.TimeBucket{Metrics: domain.Metrics{RequestCount: testInt64Pointer(1), SuccessRatio: testFloat64Pointer(1)}}, wantRows: 1},
		{name: "count free heartbeat stays visible", coverageStart: -24 * time.Hour, modelAge: 25 * time.Hour, state: domain.ServiceHealthy, bucket: &domain.TimeBucket{Metrics: domain.Metrics{SuccessRatio: testFloat64Pointer(1)}}, wantRows: 1},
		{name: "zero count bucket does not keep card visible", coverageStart: -24 * time.Hour, modelAge: 25 * time.Hour, state: domain.ServiceNoSamples, bucket: &domain.TimeBucket{Metrics: domain.Metrics{RequestCount: testInt64Pointer(0), SuccessRatio: testFloat64Pointer(1)}}, wantRows: 0},
		{name: "failed acquisition stays visible", coverageStart: -24 * time.Hour, modelAge: 25 * time.Hour, state: domain.ServiceNoSamples, failedSite: true, wantRows: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dbStore := openTestStore(t)
			ctx := context.Background()
			site := createTestSite(t, dbStore)
			now := time.Now().UTC().Truncate(time.Millisecond)
			model := domain.ModelObservation{
				RawName: "gpt-5.6-sol",
				Groups:  []domain.GroupObservation{{RawName: "default", ServiceState: test.state, Metrics: test.metrics}},
			}
			if test.coverageStart != 0 {
				model.HistoryCoverageStart = now.Add(test.coverageStart)
				model.HistoryCoverageEnd = now
			}
			if test.bucket != nil {
				bucket := *test.bucket
				bucket.Start = now.Add(-time.Hour)
				bucket.End = now
				bucket.Resolution = time.Hour
				model.Groups[0].Buckets = []domain.TimeBucket{bucket}
			}
			collection := domain.Collection{
				SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true,
				Models: []domain.ModelObservation{model},
			}
			if _, _, err := dbStore.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
				t.Fatal(err)
			}
			if _, err := dbStore.DB().Exec(`UPDATE raw_models SET first_seen_at = ? WHERE site_id = ?`, now.Add(-test.modelAge).UnixMilli(), site.ID); err != nil {
				t.Fatal(err)
			}
			if err := dbStore.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			rules, err := dbStore.ListRules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := matcher.New(rules)
			if err != nil {
				t.Fatal(err)
			}
			if err := dbStore.RefreshAllMatches(ctx, engine, now); err != nil {
				t.Fatal(err)
			}
			if test.failedSite {
				if err := dbStore.SetAcquisitionState(ctx, site.ID, domain.AcquisitionCollectionFailed, now); err != nil {
					t.Fatal(err)
				}
			}

			rows, err := dbStore.QueryPublicRows(ctx, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != test.wantRows {
				t.Fatalf("public rows = %d, want %d: %+v", len(rows), test.wantRows, rows)
			}
		})
	}
}

func TestHiddenNoSampleModelReappearsAfterSample(t *testing.T) {
	dbStore := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, dbStore)
	now := time.Now().UTC().Truncate(time.Millisecond)
	model := domain.ModelObservation{
		RawName: "gpt-5.6-sol", HistoryCoverageStart: now.Add(-24 * time.Hour), HistoryCoverageEnd: now,
		Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceNoSamples}},
	}
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: []domain.ModelObservation{model}}
	if _, _, err := dbStore.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	if _, err := dbStore.DB().Exec(`UPDATE raw_models SET first_seen_at = ? WHERE site_id = ?`, now.Add(-25*time.Hour).UnixMilli(), site.ID); err != nil {
		t.Fatal(err)
	}
	if err := dbStore.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules, err := dbStore.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := dbStore.RefreshAllMatches(ctx, engine, now); err != nil {
		t.Fatal(err)
	}
	rows, err := dbStore.QueryPublicRows(ctx, "", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty covered model remained visible: %+v, %v", rows, err)
	}

	next := now.Add(time.Minute)
	model.HistoryCoverageStart = next.Add(-24 * time.Hour)
	model.HistoryCoverageEnd = next
	model.Groups[0].ServiceState = domain.ServiceHealthy
	model.Groups[0].Buckets = []domain.TimeBucket{{
		Start: next.Add(-time.Minute), End: next, Resolution: time.Minute,
		Metrics: domain.Metrics{RequestCount: testInt64Pointer(1), SuccessRatio: testFloat64Pointer(1)},
	}}
	collection.ObservedAt, collection.CollectedAt, collection.Models = next, next, []domain.ModelObservation{model}
	if _, _, err := dbStore.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	rows, err = dbStore.QueryPublicRows(ctx, "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("sampled model did not reappear: %+v, %v", rows, err)
	}
}

func testInt64Pointer(value int64) *int64       { return &value }
func testFloat64Pointer(value float64) *float64 { return &value }

func TestPublicStateChangesAdvanceRevision(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	assertRevision := func(want int64) {
		t.Helper()
		got, err := store.Revision(ctx)
		if err != nil || got != want {
			t.Fatalf("revision = %d, want %d, err=%v", got, want, err)
		}
	}
	assertRevision(0)
	if err := store.SetAcquisitionState(ctx, site.ID, domain.AcquisitionCollecting, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertRevision(0)
	if err := store.SetAcquisitionState(ctx, site.ID, domain.AcquisitionCollectionFailed, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertRevision(1)
	if err := store.UpdateSite(ctx, site.ID, "Updated", site.AdapterKey, site.AdapterConfig, false, &site.SessionRequired, 15*time.Minute, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertRevision(2)
	if err := store.BumpRevision(ctx); err != nil {
		t.Fatal(err)
	}
	assertRevision(3)
}

func TestRecoverRunningCollectionRunsAfterRestart(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	site, err := store.CreateSite(ctx, Site{Name: "interrupted", BaseURL: "https://interrupted.example", SourceURL: "https://interrupted.example/status", AdapterKey: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, time.August, 24, 3, 0, 0, 0, time.UTC)
	runID, err := store.StartCollectionRun(ctx, site.ID, site.AdapterKey, started)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAcquisitionState(ctx, site.ID, domain.AcquisitionCollecting, started); err != nil {
		t.Fatal(err)
	}
	finished := started.Add(time.Minute)
	count, err := store.RecoverRunningCollectionRuns(ctx, finished)
	if err != nil || count != 1 {
		t.Fatalf("recovered runs = %d, err = %v", count, err)
	}
	runs, err := store.ListCollectionRuns(ctx, 10)
	if err != nil || len(runs) != 1 || runs[0].ID != runID || runs[0].Status != "failed" || runs[0].ErrorCode != "process_restarted" {
		t.Fatalf("recovered run = %+v, err = %v", runs, err)
	}
	recovered, err := store.GetSite(ctx, site.ID)
	if err != nil || recovered.AcquisitionState != domain.AcquisitionCollectionFailed {
		t.Fatalf("recovered site = %+v, err = %v", recovered, err)
	}
}

func TestPublicHistoryReturnsOnlyMatchedRecentBuckets(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	ratio := 92.0
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: []domain.ModelObservation{{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "plus", ServiceState: domain.ServiceHealthy, Buckets: []domain.TimeBucket{
		{Start: now.Add(-25 * time.Hour), End: now.Add(-24*time.Hour - time.Minute), Resolution: time.Hour, Metrics: domain.Metrics{SuccessRatio: &ratio}},
		{Start: now.Add(-time.Hour), End: now, Resolution: time.Hour, Metrics: domain.Metrics{SuccessRatio: &ratio}},
		{Start: now.Add(25 * time.Hour), End: now.Add(26 * time.Hour), Resolution: time.Hour, Metrics: domain.Metrics{SuccessRatio: &ratio}},
	}}}}}}
	if _, _, err := store.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllMatches(ctx, engine, now); err != nil {
		t.Fatal(err)
	}

	buckets, err := store.QueryPublicHistory(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("history buckets = %d, want 2 half-hour slots", len(buckets))
	}
	if buckets[0].SiteID != site.ID || buckets[0].RawModelName != "gpt-5.6-sol" {
		t.Fatalf("unexpected history bucket: %+v", buckets[0])
	}
	if buckets[0].ServiceState != domain.ServiceHealthy {
		t.Fatalf("history semantics lost: %+v", buckets[0])
	}
}

func TestPublicHistoryCollapsesGroupsIntoBestHalfHourState(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	healthy, failed := 0.95, 0.10
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: []domain.ModelObservation{{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{
		{RawName: "healthy", ServiceState: domain.ServiceHealthy, Buckets: []domain.TimeBucket{{Start: now.Add(-20 * time.Minute), End: now, Resolution: 20 * time.Minute, Metrics: domain.Metrics{SuccessRatio: &healthy}}}},
		{RawName: "failed", ServiceState: domain.ServiceFailed, Buckets: []domain.TimeBucket{{Start: now.Add(-20 * time.Minute), End: now, Resolution: 20 * time.Minute, Metrics: domain.Metrics{SuccessRatio: &failed}}}},
	}}}}
	if _, _, err := store.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRule(ctx, matcher.Rule{Provider: "OpenAI", CanonicalName: "gpt-5.6-sol", RequiredTerms: []string{"gpt", "5", "6", "sol"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllMatches(ctx, engine, now); err != nil {
		t.Fatal(err)
	}

	buckets, err := store.QueryPublicHistory(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].ServiceState != domain.ServiceHealthy {
		t.Fatalf("collapsed history = %+v, want one healthy slot", buckets)
	}
}

func TestRefreshMatchesKeepsConflictsWithoutPrimary(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	now := time.Now().UTC()
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true, Models: []domain.ModelObservation{{RawName: "model-pro", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceHealthy}}}}}
	if _, _, err := store.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	rules := []matcher.Rule{
		{Provider: "test", CanonicalName: "model", RequiredTerms: []string{"model"}, Priority: 10, Enabled: true},
		{Provider: "test", CanonicalName: "model-pro", RequiredTerms: []string{"model", "pro"}, Priority: 20, Enabled: true},
	}
	for _, rule := range rules {
		if err := store.CreateRule(ctx, rule); err != nil {
			t.Fatal(err)
		}
	}
	storedRules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(storedRules)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllMatches(ctx, engine, now); err != nil {
		t.Fatal(err)
	}
	var primaryCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM model_matches WHERE is_primary = 1`).Scan(&primaryCount); err != nil {
		t.Fatal(err)
	}
	if primaryCount != 0 {
		t.Fatalf("conflict has %d primary matches", primaryCount)
	}
	conflicts, err := store.ListMatchConflicts(ctx, 10)
	if err != nil || len(conflicts) != 1 || len(conflicts[0].CandidateRules) != 2 {
		t.Fatalf("conflicts = %+v, err=%v", conflicts, err)
	}
	publicRows, err := store.QueryPublicRows(ctx, "", "")
	if err != nil || len(publicRows) != 0 {
		t.Fatalf("conflicted model leaked publicly: %+v, err=%v", publicRows, err)
	}
}

func TestPublicRowsExposePricingFromSourceExtensions(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	if err := store.CreateRule(ctx, matcher.Rule{Provider: "test", CanonicalName: "gpt-5.5", RequiredTerms: []string{"gpt", "5", "5"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	input, output, cacheRead, cacheWrite, multiplier := 2.0, 6.0, 0.2, 2.5, 0.5
	collection := domain.Collection{
		SiteID: site.ID, ObservedAt: time.Now().UTC(), CollectedAt: time.Now().UTC(), CatalogComplete: true,
		Models: []domain.ModelObservation{{
			RawName: "gpt-5.5", Extension: pricing.ModelExtension(pricing.ModelPrice{RawName: "gpt-5.5"}),
			Groups: []domain.GroupObservation{{RawName: "free", ServiceState: domain.ServiceHealthy, Extension: pricing.GroupExtension(pricing.DisplayPrice{Available: true, Currency: "USD", CurrencySymbol: "$", InputPerMillion: &input, OutputPerMillion: &output, CacheReadPerMillion: &cacheRead, CacheWritePerMillion: &cacheWrite, GroupMultiplier: &multiplier})}},
		}},
	}
	if _, _, err := store.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllMatches(ctx, engine, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rows, err := store.QueryPublicRows(ctx, "", "")
	if err != nil || len(rows) != 1 || rows[0].Price == nil || rows[0].Price.InputPerMillion == nil || *rows[0].Price.InputPerMillion != 2 ||
		rows[0].Price.CacheReadPerMillion == nil || *rows[0].Price.CacheReadPerMillion != cacheRead ||
		rows[0].Price.CacheWritePerMillion == nil || *rows[0].Price.CacheWritePerMillion != cacheWrite || *rows[0].Price.GroupMultiplier != 0.5 {
		t.Fatalf("pricing row = %+v, err=%v", rows, err)
	}
}

func TestPublicDetailGroupsExposeCurrentPricingWithoutHistory(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	site := createTestSite(t, store)
	if err := store.CreateRule(ctx, matcher.Rule{Provider: "test", CanonicalName: "gpt-5-nano", RequiredTerms: []string{"gpt", "5", "nano"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	input, output, multiplier := 0.25, 1.0, 0.5
	now := time.Now().UTC()
	collection := domain.Collection{
		SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true,
		Models: []domain.ModelObservation{{
			RawName: "gpt-5-nano",
			Groups: []domain.GroupObservation{{
				RawName: "free", ServiceState: domain.ServiceNoSamples,
				Extension: pricing.GroupExtension(pricing.DisplayPrice{
					Available: true, Currency: "USD", CurrencySymbol: "$",
					InputPerMillion: &input, OutputPerMillion: &output, GroupMultiplier: &multiplier,
				}),
			}},
		}},
	}
	if _, _, err := store.ApplyCollection(ctx, collection, strings.ToLower); err != nil {
		t.Fatal(err)
	}
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := matcher.New(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshAllMatches(ctx, engine, now); err != nil {
		t.Fatal(err)
	}

	buckets, err := store.QueryPublicDetails(ctx, "", site.Name, "gpt-5-nano", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 0 {
		t.Fatalf("history buckets = %+v, want none", buckets)
	}
	groups, err := store.QueryPublicDetailGroups(ctx, "", site.Name, "gpt-5-nano")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Price == nil || groups[0].Price.GroupMultiplier == nil || *groups[0].Price.GroupMultiplier != multiplier {
		t.Fatalf("detail groups = %+v, want current priced group", groups)
	}
	if groups[0].ServiceState != domain.ServiceNoSamples || groups[0].AcquisitionState != domain.AcquisitionFresh {
		t.Fatalf("detail group state = %+v", groups[0])
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relaypulse.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func createTestSite(t *testing.T, store *Store) Site {
	t.Helper()
	site, err := store.CreateSite(context.Background(), Site{
		Name: "Test", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing",
		AdapterKey: "test", Enabled: true, Interval: 20 * time.Minute, Jitter: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	return site
}

func assertCount(t *testing.T, store *Store, table string, expected int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != expected {
		t.Fatalf("%s count = %d, want %d", table, count, expected)
	}
}

func assertRemovalEvidence(t *testing.T, store *Store, expectedRuns int, removed bool) {
	t.Helper()
	var runs int
	var removedAt *int64
	if err := store.DB().QueryRow(`SELECT absent_complete_runs, removed_at FROM raw_models`).Scan(&runs, &removedAt); err != nil {
		t.Fatalf("read removal evidence: %v", err)
	}
	if runs != expectedRuns || (removedAt != nil) != removed {
		t.Fatalf("removal evidence = (%d, %v), want (%d, %v)", runs, removedAt, expectedRuns, removed)
	}
}
