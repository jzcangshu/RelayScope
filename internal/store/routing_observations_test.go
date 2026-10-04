package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"relayscope/internal/domain"
	"relayscope/internal/pricing"
	"relayscope/internal/routing"
)

func routingTestStore(t *testing.T) *Store {
	t.Helper()
	value, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	value.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = value.Close() })
	_, err = value.db.Exec(`INSERT INTO sites(id,name,base_url,source_url,adapter_key,interval_seconds,created_at,updated_at) VALUES (1,'A','https://a.example.test','https://a.example.test','newapi-pricing',900,1,1),(2,'B','https://b.example.test','https://b.example.test','newapi-pricing',900,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRoutingObservationsLegacyNoFallbackAndSourceTime(t *testing.T) {
	db := routingTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 4, 4, 0, 0, 0, time.UTC)
	unit, input, output, known, included := "USD", "0.1", "0.2", true, true
	observed := at.Add(-45 * time.Minute).UnixMilli()
	price := routing.Price{Kind: "per-token", Source: "exact-group", MoneyUnit: &unit, InputPerMillion: &input, OutputPerMillion: &output, ProtocolScope: routing.ProtocolScope{Kind: "explicit", Protocols: []string{"openai-completions"}}, IncludesGroupMultiplier: &included, PriceObservedAt: &observed}
	model := pricing.ModelPrice{RawName: "M", GroupMultipliers: map[string]float64{"cheap": 0.1}}
	modelRatio, quota := 1.0, 500000.0
	model.ModelRatio, model.QuotaPerUnit = &modelRatio, &quota
	collection := domain.Collection{SiteID: 1, ObservedAt: at, CollectedAt: at, Models: []domain.ModelObservation{{RawName: "M", Extension: pricing.ModelExtension(model), Groups: []domain.GroupObservation{
		{RawName: "missing", ServiceState: domain.ServiceHealthy},
		{RawName: "vip", ServiceState: domain.ServiceHealthy, Extension: routing.WriteExtension(nil, routing.Extension{GroupKnown: &known, Price: &price})},
	}}}}
	if _, _, err := db.ApplyCollection(ctx, collection, nil); err != nil {
		t.Fatal(err)
	}
	envelope, err := db.QueryRoutingObservations(ctx, []int64{1}, at)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.Revision != "1" || len(envelope.Observations) != 2 {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	for _, row := range envelope.Observations {
		if row.RawGroupName == nil {
			if row.Price != nil || row.Availability.Evidence != "unknown" {
				t.Fatalf("legacy data must not borrow cheap model price: %+v", row)
			}
		} else if *row.RawGroupName == "vip" {
			if row.Price == nil || *row.Price.PriceObservedAt != observed || *row.Price.SuggestedIntervalMilliseconds != 900000 {
				t.Fatalf("source time was washed into response time: %+v", row)
			}
		} else {
			t.Fatalf("unexpected group: %+v", row)
		}
	}
	identity, err := db.QueryRoutingObservations(ctx, nil, at.Add(time.Hour))
	if err != nil || identity.SourceInstanceID != envelope.SourceInstanceID || len(identity.Observations) != 0 {
		t.Fatalf("empty scope must only return stable identity: %+v %v", identity, err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil || len(encoded) == 0 {
		t.Fatal(err)
	}
	t.Logf("RoutingContractSample=%s", encoded)
}

func TestRoutingIdentitySurvivesReopenAndResponseHasRowBound(t *testing.T) {
	// Keep the disk fixture: the workspace forbids deleting files.
	directory, err := os.MkdirTemp("", "relayscope-routing-preserved-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "routing-test.db")
	first, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := first.QueryRoutingObservations(context.Background(), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	other, err := second.QueryRoutingObservations(context.Background(), nil, time.Now())
	if err != nil || other.SourceInstanceID != identity.SourceInstanceID {
		t.Fatalf("identity changed after reopen: %+v %v", other, err)
	}
	t.Logf("preserved test fixture: %s", directory)
	db := routingTestStore(t)
	_, err = db.db.Exec(`INSERT INTO raw_models(id,site_id,raw_name,normalized_name,first_seen_at,last_seen_at) VALUES (1,1,'M','m',1,1);
		WITH RECURSIVE counter(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM counter WHERE n<20001)
		INSERT INTO site_groups(id,raw_model_id,raw_name,first_seen_at,last_seen_at) SELECT n,1,CAST(n AS TEXT),1,1 FROM counter;
		INSERT INTO current_snapshots(group_id,service_state,observed_at,collected_at) SELECT id,'unknown',1,1 FROM site_groups;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.QueryRoutingObservations(context.Background(), []int64{1}, time.Now()); err == nil {
		t.Fatal("oversized response must fail as a whole")
	}
}

func TestRoutingObservationsScopeIsAllOrNothing(t *testing.T) {
	db := routingTestStore(t)
	for _, scope := range [][]int64{{1, 999}, {1, 1}, {0}, {-1}} {
		if _, err := db.QueryRoutingObservations(context.Background(), scope, time.Now()); err == nil {
			t.Fatalf("invalid scope accepted: %v", scope)
		}
	}
	if _, err := db.db.Exec(`UPDATE sites SET enabled=0 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.QueryRoutingObservations(context.Background(), []int64{1, 2}, time.Now()); err == nil {
		t.Fatal("disabled site returned partial success")
	}
}
