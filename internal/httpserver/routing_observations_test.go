package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relayscope/internal/routing"
	"relayscope/internal/store"
)

func TestRoutingObservationRouteIdentityAndInvalidScope(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.DB().SetMaxOpenConns(1)
	defer db.Close()
	now := time.Date(2026, 10, 4, 4, 0, 0, 0, time.UTC)
	handler, err := NewHandler(Options{Store: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/routing-observations", nil))
	if response.Code != 200 {
		t.Fatalf("identity request: %d %s", response.Code, response.Body)
	}
	var envelope routing.Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.SourceInstanceID == "" || envelope.GeneratedAt != now.UnixMilli() || len(envelope.RequestedSiteIDs) != 0 || len(envelope.Observations) != 0 {
		t.Fatalf("invalid identity response: %+v", envelope)
	}
	for _, query := range []string{"?siteId=1&siteId=1", "?siteId=0", "?siteId=-1", "?siteId=01", "?siteId=unknown", "?siteId=1"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/routing-observations"+query, nil))
		if response.Code != 400 {
			t.Fatalf("invalid scope %s: %d %s", query, response.Code, response.Body)
		}
	}
}

func TestRoutingObservationRouteRejectsOversizedResponse(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.DB().SetMaxOpenConns(1)
	defer db.Close()
	_, err = db.DB().Exec(`INSERT INTO sites(id,name,base_url,source_url,adapter_key,interval_seconds,created_at,updated_at)
		VALUES (1,'fixture','https://example.test','https://example.test','newapi-pricing',900,1,1);
		INSERT INTO raw_models(id,site_id,raw_name,normalized_name,first_seen_at,last_seen_at)
		VALUES (1,1,?,'fixture',1,1);
		INSERT INTO site_groups(id,raw_model_id,raw_name,first_seen_at,last_seen_at) VALUES (1,1,'vip',1,1);
		INSERT INTO current_snapshots(group_id,service_state,observed_at,collected_at) VALUES (1,'unknown',1,1);`, strings.Repeat("M", routing.MaxBytes))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Store: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/routing-observations?siteId=1", nil))
	if response.Code != http.StatusRequestEntityTooLarge || response.Body.Len() >= routing.MaxBytes {
		t.Fatalf("oversized data must fail before publishing any rows: %d, %d bytes", response.Code, response.Body.Len())
	}
}

func TestRoutingObservationRouteRejectsMalformedQueryAsAWhole(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.DB().SetMaxOpenConns(1)
	defer db.Close()
	_, err = db.DB().Exec(`INSERT INTO sites(id,name,base_url,source_url,adapter_key,interval_seconds,created_at,updated_at) VALUES (1,'fixture','https://example.test','https://example.test','newapi-pricing',900,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Store: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?siteId=1&siteId=%ZZ", "?siteId=1&siteId=2;siteId=3", "?unexpected=%ZZ"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/routing-observations"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed scope partially accepted: %s, %d", query, response.Code)
		}
	}
}
