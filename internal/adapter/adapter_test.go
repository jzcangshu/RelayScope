package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relaypulse/internal/domain"
)

type fakeChallenge struct{ calls int }

func (challenge *fakeChallenge) Solve(_ context.Context, _ string) (ChallengeResult, error) {
	challenge.calls++
	return ChallengeResult{UserAgent: "solved-agent", Cookies: []ChallengeCookie{{Name: "cf_clearance", Value: "ok"}}}, nil
}

func TestHTTPFetcherRedactsQueryValuesFromErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte("not-json"))
	}))
	defer server.Close()

	var target map[string]any
	err := (HTTPFetcher{Client: server.Client()}).GetJSON(context.Background(), server.URL+"/status?api_key=secret-value", &target)
	if err == nil || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "api_key=") {
		t.Fatalf("query value leaked through decode error: %v", err)
	}
}

func TestHTTPFetcherRetriesForbiddenWithChallengeSession(t *testing.T) {
	var sawCookie, sawAgent bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Cookie") == "cf_clearance=ok" {
			sawCookie = true
		}
		if request.Header.Get("User-Agent") == "solved-agent" {
			sawAgent = true
		}
		if sawCookie && sawAgent {
			writer.Header().Set("Content-Type", "application/json")
			writer.Write([]byte(`{"ok":true}`))
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		writer.WriteHeader(http.StatusForbidden)
		writer.Write([]byte("Just a moment... Cloudflare challenge"))
	}))
	defer server.Close()
	challenge := &fakeChallenge{}
	fetcher := HTTPFetcher{Client: server.Client(), Challenge: challenge, UserAgent: "base"}
	body, _, err := fetcher.GetBytes(context.Background(), server.URL)
	if err != nil || string(body) != `{"ok":true}` || challenge.calls != 1 || !sawCookie || !sawAgent {
		t.Fatalf("challenge retry failed: body=%s err=%v calls=%d cookie=%v agent=%v", body, err, challenge.calls, sawCookie, sawAgent)
	}
}

func TestHTTPFetcherDoesNotSolveOrdinaryForbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		writer.Write([]byte("permission denied"))
	}))
	defer server.Close()
	challenge := &fakeChallenge{}
	_, _, err := (HTTPFetcher{Client: server.Client(), Challenge: challenge}).GetBytes(context.Background(), server.URL)
	if err == nil || challenge.calls != 0 {
		t.Fatalf("ordinary forbidden should not invoke challenge solver: err=%v calls=%d", err, challenge.calls)
	}
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Challenge {
		t.Fatalf("ordinary forbidden mislabeled as challenge: %#v", err)
	}
}

func TestHTTPFetcherSolvesExplicitChallengeOnServiceUnavailable(t *testing.T) {
	challenge := &fakeChallenge{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Cookie") == "cf_clearance=ok" {
			writer.Write([]byte(`{"ok":true}`))
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		writer.WriteHeader(http.StatusServiceUnavailable)
		writer.Write([]byte(`<title>Just a moment...</title><script src="/cdn-cgi/challenge-platform/x.js"></script>`))
	}))
	defer server.Close()

	body, _, err := (HTTPFetcher{Client: server.Client(), Challenge: challenge}).GetBytes(context.Background(), server.URL)
	if err != nil || string(body) != `{"ok":true}` || challenge.calls != 1 || requests != 2 {
		t.Fatalf("service unavailable challenge was not recovered: body=%s err=%v calls=%d requests=%d", body, err, challenge.calls, requests)
	}
}

func TestHTTPFetcherMarksRepeatedChallengeFailed(t *testing.T) {
	challenge := &fakeChallenge{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		writer.WriteHeader(http.StatusTooManyRequests)
		writer.Write([]byte(`<div class="cf-turnstile">verify</div>`))
	}))
	defer server.Close()

	_, _, err := (HTTPFetcher{Client: server.Client(), Challenge: challenge}).GetBytes(context.Background(), server.URL)
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) || !fetchErr.Challenge || !fetchErr.ChallengeFailed || challenge.calls != 1 {
		t.Fatalf("repeated challenge classification = %#v, calls=%d", fetchErr, challenge.calls)
	}
}

func TestHTTPFetcherMergesSiteCookiesAfterChallenge(t *testing.T) {
	var cookie string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie = request.Header.Get("Cookie")
		if strings.Contains(cookie, "sid=site") && strings.Contains(cookie, "cf_clearance=ok") {
			writer.Write([]byte("ok"))
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		writer.WriteHeader(http.StatusForbidden)
		writer.Write([]byte("cloudflare challenge"))
	}))
	defer server.Close()
	challenge := &fakeChallenge{}
	_, _, err := (HTTPFetcher{Client: server.Client(), Challenge: challenge, Cookies: []ChallengeCookie{{Name: "sid", Value: "site"}}}).GetBytes(context.Background(), server.URL)
	if err != nil || !strings.Contains(cookie, "sid=site") || !strings.Contains(cookie, "cf_clearance=ok") {
		t.Fatalf("challenge cookie merge failed: err=%v cookie=%q", err, cookie)
	}
}

func TestHTTPFetcherPreservesHeadersAfterChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") == "Bearer token" && request.Header.Get("New-API-User") == "42" && strings.Contains(request.Header.Get("Cookie"), "cf_clearance=ok") {
			writer.Write([]byte("ok"))
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		writer.WriteHeader(http.StatusForbidden)
		writer.Write([]byte("cloudflare challenge"))
	}))
	defer server.Close()
	challenge := &fakeChallenge{}
	_, _, err := (HTTPFetcher{Client: server.Client(), Challenge: challenge, Headers: map[string]string{"Authorization": "Bearer token", "New-API-User": "42"}}).GetBytes(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("challenge retry dropped authentication headers: %v", err)
	}
}

type fakeFetcher struct {
	responses map[string][]byte
}

func (fetcher fakeFetcher) GetJSON(_ context.Context, rawURL string, target any) error {
	body, exists := fetcher.responses[rawURL]
	if !exists {
		return &missingResponseError{rawURL: rawURL}
	}
	return json.Unmarshal(body, target)
}
func (fetcher fakeFetcher) GetBytes(_ context.Context, rawURL string) ([]byte, http.Header, error) {
	body, exists := fetcher.responses[rawURL]
	if !exists {
		return nil, nil, &missingResponseError{rawURL: rawURL}
	}
	return body, http.Header{}, nil
}

type missingResponseError struct{ rawURL string }

func (err *missingResponseError) Error() string { return "missing response: " + err.rawURL }

func TestNewAPIAdapterParsesCatalogWithoutPresentationPagination(t *testing.T) {
	t.Parallel()

	adapter := NewAPIAdapter{}
	response := []byte(`{"data":[{"model":"deepseek-ai/deepseek-v4-flash-0731","provider":"DeepSeek","group":"default","success_rate":0.91,"latency":820,"tps":70},{"model":"gpt-5.6-sol","provider":"OpenAI","group":"free","success_rate":0.99,"latency":210,"tps":120}]}`)
	collection, err := adapter.Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"pricingPath":"/api/pricing"}`}, fakeFetcher{responses: map[string][]byte{"https://example.test/api/pricing": response}}, time.Now())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !collection.CatalogComplete || len(collection.Models) != 2 {
		t.Fatalf("unexpected catalog: %+v", collection)
	}
	if collection.Models[0].RawName != "deepseek-ai/deepseek-v4-flash-0731" || collection.Models[0].Groups[0].RawName != "default" {
		t.Fatalf("raw identity lost: %+v", collection.Models[0])
	}
}

func TestNewAPIAdapterMapsEmptyGroupToDefaultAndNoSamples(t *testing.T) {
	t.Parallel()

	collection, err := (NewAPIAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{"https://example.test/api/pricing": []byte(`{"data":[{"model":"model"}]}`)}}, time.Now())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	group := collection.Models[0].Groups[0]
	if group.RawName != "default" || group.ServiceState != domain.ServiceNoSamples {
		t.Fatalf("unexpected empty metrics mapping: %+v", group)
	}
}

func TestNewAPIAdapterMapsPresenceCatalogToAvailability(t *testing.T) {
	t.Parallel()

	collection, err := (NewAPIAdapter{}).Collect(context.Background(), Site{
		ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"availabilityMode":"presence"}`,
	}, fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/pricing": []byte(`{"data":[{"model":"gpt-5.6-sol"}]}`),
	}}, time.Now())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if collection.MissingCatalogState != domain.ServiceFailed {
		t.Fatalf("missing catalog state = %q, want failed", collection.MissingCatalogState)
	}
	group := collection.Models[0].Groups[0]
	if group.ServiceState != domain.ServiceHealthy {
		t.Fatalf("present model state = %q, want healthy", group.ServiceState)
	}
}

func TestNewAPIAdapterAcceptsWrappedCatalogAndRejectsEmptyModels(t *testing.T) {
	wrapped := []byte(`{"result":{"models":[{"name":"vendor/gpt-5.6-sol","successRatio":"98%"}]}}`)
	collection, err := (NewAPIAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{"https://example.test/api/pricing": wrapped}}, time.Now())
	if err != nil || len(collection.Models) != 1 || collection.Models[0].RawName != "vendor/gpt-5.6-sol" {
		t.Fatalf("wrapped catalog parse failed: %+v %v", collection, err)
	}
	if collection.Models[0].Groups[0].Metrics.SuccessRatio == nil || *collection.Models[0].Groups[0].Metrics.SuccessRatio != 0.98 {
		t.Fatalf("ratio normalization failed: %+v", collection.Models[0].Groups[0].Metrics)
	}
	_, err = (NewAPIAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{"https://example.test/api/pricing": []byte(`{"data":[{"name":""}]}`)}}, time.Now())
	if err == nil {
		t.Fatal("empty model catalog should fail")
	}
}

func TestNewAPIAdapterParsesRealPricingGroups(t *testing.T) {
	body := []byte(`{"data":[{"model_name":"vendor/gpt-5.6-sol-free","enable_groups":["free","vip"]}]}`)
	collection, err := (NewAPIAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{"https://example.test/api/pricing": body}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	groups := collection.Models[0].Groups
	if len(groups) != 2 || groups[0].RawName != "free" || groups[1].RawName != "vip" {
		t.Fatalf("real pricing groups lost: %+v", groups)
	}
}

func TestNewAPIAdapterPreservesPricingMetadata(t *testing.T) {
	responses := map[string][]byte{
		"https://example.test/api/pricing": []byte(`{"group_ratio":{"free":0.5},"data":[{"model_name":"gpt-5.5","quota_type":0,"model_ratio":2,"completion_ratio":3,"enable_groups":["free"]}]}`),
		"https://example.test/api/status":  []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_symbol":"$"}}`),
	}
	collection, err := (NewAPIAdapter{}).Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: responses}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model := collection.Models[0]
	if len(model.Extension) == 0 || len(model.Groups) != 1 || len(model.Groups[0].Extension) == 0 {
		t.Fatalf("pricing metadata missing: model=%s group=%s", model.Extension, model.Groups[0].Extension)
	}
	if !strings.Contains(string(model.Groups[0].Extension), `"inputPerMillion":2`) || !strings.Contains(string(model.Groups[0].Extension), `"groupMultiplier":0.5`) {
		t.Fatalf("normalized price missing: %s", model.Groups[0].Extension)
	}
}

func TestNewAPIAdapterCanSkipUnavailableDetails(t *testing.T) {
	collection := domain.Collection{Models: []domain.ModelObservation{{RawName: "gpt-5-nano"}}}
	err := (NewAPIAdapter{}).CollectDetails(
		context.Background(),
		Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"skipDetails":true}`},
		fakeFetcher{responses: map[string][]byte{}},
		&collection,
		[]string{"gpt-5-nano"},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("skip details: %v", err)
	}
	if !collection.Models[0].HistoryCoverageStart.IsZero() || !collection.Models[0].HistoryCoverageEnd.IsZero() {
		t.Fatalf("skipped details declared history coverage: %+v", collection.Models[0])
	}
}

func TestNewAPIAdapterDeclaresHistoryCoverageForEmptyDetails(t *testing.T) {
	now := time.Date(2026, time.August, 16, 6, 0, 0, 0, time.UTC)
	collection := domain.Collection{Models: []domain.ModelObservation{{
		RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceNoSamples}},
	}}}
	fetcher := fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/perf-metrics?hours=24&model=gpt-5.6-sol": []byte(`{"data":[]}`),
	}}

	if err := (NewAPIAdapter{}).CollectDetails(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fetcher, &collection, []string{"gpt-5.6-sol"}, now); err != nil {
		t.Fatal(err)
	}
	model := collection.Models[0]
	if !model.HistoryCoverageStart.Equal(now.Add(-24*time.Hour)) || !model.HistoryCoverageEnd.Equal(now) {
		t.Fatalf("empty detail coverage = (%s, %s)", model.HistoryCoverageStart, model.HistoryCoverageEnd)
	}
}

func TestNewAPIAdapterKeepsPartialDetailSuccess(t *testing.T) {
	now := time.Date(2026, time.August, 16, 6, 0, 0, 0, time.UTC)
	collection := domain.Collection{Models: []domain.ModelObservation{
		{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceNoSamples}}},
		{RawName: "deepseek-v4-pro", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceNoSamples}}},
	}}
	fetcher := fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/perf-metrics?hours=24&model=gpt-5.6-sol": []byte(`{"data":[{"bucket_start":"2026-08-16T05:00:00Z","bucket_end":"2026-08-16T06:00:00Z","request_count":2,"success_count":2,"failure_count":0,"success_rate":100}]}`),
	}}

	err := (NewAPIAdapter{}).CollectDetails(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fetcher, &collection, []string{"gpt-5.6-sol", "deepseek-v4-pro"}, now)
	if err != nil {
		t.Fatalf("partial detail collection failed: %v", err)
	}
	if len(collection.Issues) != 1 || collection.Issues[0].Code != "detail_fetch_failed" || collection.Issues[0].Scope != "deepseek-v4-pro" {
		t.Fatalf("partial issues = %+v", collection.Issues)
	}
	if len(collection.Models[0].Groups[0].Buckets) != 1 || collection.Models[0].Groups[0].ServiceState != domain.ServiceHealthy {
		t.Fatalf("successful detail was discarded: %+v", collection.Models[0])
	}
	if !collection.Models[0].HistoryCoverageStart.Equal(now.Add(-24*time.Hour)) || !collection.Models[0].HistoryCoverageEnd.Equal(now) {
		t.Fatalf("successful detail coverage = %+v", collection.Models[0])
	}
	if !collection.Models[1].HistoryCoverageStart.IsZero() || !collection.Models[1].HistoryCoverageEnd.IsZero() {
		t.Fatalf("failed detail declared coverage: %+v", collection.Models[1])
	}
}

func TestNewAPIAdapterFailsWhenAllDetailsFail(t *testing.T) {
	collection := domain.Collection{Models: []domain.ModelObservation{
		{RawName: "gpt-5.6-sol", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceNoSamples}}},
		{RawName: "deepseek-v4-pro", Groups: []domain.GroupObservation{{RawName: "default", ServiceState: domain.ServiceNoSamples}}},
	}}
	err := (NewAPIAdapter{}).CollectDetails(
		context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fakeFetcher{responses: map[string][]byte{}},
		&collection, []string{"gpt-5.6-sol", "deepseek-v4-pro"}, time.Now().UTC(),
	)
	if err == nil || len(collection.Issues) != 2 {
		t.Fatalf("total detail failure = %v, issues=%+v", err, collection.Issues)
	}
}

func TestDetailIssueDoesNotPersistRequestURL(t *testing.T) {
	collection := domain.Collection{}
	appendDetailIssue(&collection, "detail_fetch_failed", "gpt-5.6-sol", &FetchError{
		URL: "https://example.test/details?sensitive=fixture-value", StatusCode: http.StatusBadGateway,
	})
	if len(collection.Issues) != 1 || collection.Issues[0].Message != "detail request returned HTTP 502" ||
		strings.Contains(collection.Issues[0].Message, "fixture-value") {
		t.Fatalf("unsafe detail diagnostic: %+v", collection.Issues)
	}
}

func TestDecodeNewAPIDetailGroupsAndSeries(t *testing.T) {
	body := []byte(`{"data":{"model_name":"gpt-oss-120b","groups":[{"group":"free","avg_ttft_ms":251,"avg_latency_ms":1191,"success_rate":90,"avg_tps":135.4,"series":[{"ts":1786485600,"avg_ttft_ms":250,"avg_latency_ms":1200,"success_rate":100,"avg_tps":140.5}]}]}}`)
	buckets, err := decodeDetailBuckets(body)
	if err != nil || len(buckets) != 2 {
		t.Fatalf("decode groups: %+v %v", buckets, err)
	}
	if !buckets[0].Aggregate || buckets[0].Group != "free" || buckets[0].SuccessRate == nil || *buckets[0].SuccessRate != 90 {
		t.Fatalf("aggregate group metrics lost: %+v", buckets[0])
	}
	bucket := buckets[1]
	if bucket.Group != "free" || bucket.Timestamp != 1786485600 || bucket.SuccessRate == nil || *bucket.SuccessRate != 100 || bucket.TPS == nil || *bucket.TPS != 140.5 {
		t.Fatalf("real detail fields lost: %+v", bucket)
	}
}

func TestMergeDetailBucketsInfersNonOverlappingResolution(t *testing.T) {
	firstRatio, secondRatio := 1.0, 0.5
	start := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	model := &domain.ModelObservation{RawName: "gpt", Groups: []domain.GroupObservation{{RawName: "free"}}}
	mergeDetailBuckets(model, []detailBucket{
		{Group: "free", Timestamp: start.Unix(), SuccessRate: &firstRatio},
		{Group: "free", Timestamp: start.Add(15 * time.Minute).Unix(), SuccessRate: &secondRatio},
	}, start.Add(time.Hour))
	if len(model.Groups[0].Buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(model.Groups[0].Buckets))
	}
	if got := model.Groups[0].Buckets[0].End.Sub(model.Groups[0].Buckets[0].Start); got != 15*time.Minute {
		t.Fatalf("first resolution = %s, want 15m", got)
	}
	if got := model.Groups[0].Buckets[1].End.Sub(model.Groups[0].Buckets[1].Start); got != 15*time.Minute {
		t.Fatalf("last resolution = %s, want 15m", got)
	}
}

func TestMergeDetailBucketsUsesLatestSeriesForCurrentState(t *testing.T) {
	aggregate, healthy, failed := 100.0, 100.0, 0.0
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	latest := now.Add(-15 * time.Minute)
	model := &domain.ModelObservation{RawName: "claude", Groups: []domain.GroupObservation{{RawName: "free"}}}

	mergeDetailBuckets(model, []detailBucket{
		{Aggregate: true, Group: "free", SuccessRate: &aggregate},
		{Group: "free", Timestamp: now.Add(-time.Hour).Unix(), SuccessRate: &healthy},
		{Group: "free", Timestamp: latest.Unix(), SuccessRate: &failed},
	}, now)

	group := model.Groups[0]
	if group.ServiceState != domain.ServiceFailed {
		t.Fatalf("current state = %q, want latest series state failed", group.ServiceState)
	}
	if !group.ObservedAt.Equal(latest) {
		t.Fatalf("observed at = %s, want %s", group.ObservedAt, latest)
	}
	if group.Metrics.SuccessRatio == nil || *group.Metrics.SuccessRatio != 1 {
		t.Fatalf("aggregate success ratio = %v, want 1", group.Metrics.SuccessRatio)
	}
}

func TestMergeDetailBucketsDoesNotTreatAggregateOnlyAsCurrent(t *testing.T) {
	aggregate := 100.0
	model := &domain.ModelObservation{RawName: "claude"}

	mergeDetailBuckets(model, []detailBucket{{Aggregate: true, Group: "free", SuccessRate: &aggregate}}, time.Now().UTC())

	group := model.Groups[0]
	if group.ServiceState != domain.ServiceNoSamples {
		t.Fatalf("aggregate-only state = %q, want no samples", group.ServiceState)
	}
	if !group.ObservedAt.IsZero() {
		t.Fatalf("aggregate-only observation time = %s, want zero", group.ObservedAt)
	}
}

func TestMergeDetailBucketsCapsSparseInferredResolution(t *testing.T) {
	firstRatio, secondRatio := 1.0, 0.0
	start := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	model := &domain.ModelObservation{RawName: "gpt"}

	mergeDetailBuckets(model, []detailBucket{
		{Group: "free", Timestamp: start.Unix(), SuccessRate: &firstRatio},
		{Group: "free", Timestamp: start.Add(5 * time.Hour).Unix(), SuccessRate: &secondRatio},
	}, start.Add(6*time.Hour))

	for index, bucket := range model.Groups[0].Buckets {
		if bucket.Resolution > time.Hour {
			t.Fatalf("bucket %d inferred resolution = %s, want at most 1h", index, bucket.Resolution)
		}
	}
}

func TestMergeDetailBucketsUsesExplicitEndTime(t *testing.T) {
	ratio := 1.0
	start := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)
	model := &domain.ModelObservation{RawName: "gpt"}
	mergeDetailBuckets(model, []detailBucket{{Group: "free", Timestamp: start.Unix(), EndTimestamp: end.Unix(), SuccessRate: &ratio}}, start.Add(time.Hour))
	if got := model.Groups[0].Buckets[0].End; !got.Equal(end) {
		t.Fatalf("end = %s, want %s", got, end)
	}
}

func TestZeroSuccessRateIsFailedNotNoSamples(t *testing.T) {
	zero := 0.0
	if state := serviceState("", &zero); state != domain.ServiceFailed {
		t.Fatalf("zero observed success rate = %q, want failed", state)
	}
	if state := serviceState("", nil); state != domain.ServiceNoSamples {
		t.Fatalf("missing success rate = %q, want no samples", state)
	}
}

func TestServiceStateUsesFinalHealthThresholds(t *testing.T) {
	for _, test := range []struct {
		ratio float64
		want  domain.ServiceState
	}{
		{ratio: 0.85, want: domain.ServiceHealthy},
		{ratio: 0.849, want: domain.ServiceDegraded},
		{ratio: 0.50, want: domain.ServiceDegraded},
		{ratio: 0.499, want: domain.ServiceFailed},
	} {
		ratio := test.ratio
		if got := serviceState("", &ratio); got != test.want {
			t.Fatalf("ratio %.3f = %q, want %q", ratio, got, test.want)
		}
	}
}

func TestDecodeNewAPISummaryWrapper(t *testing.T) {
	body := []byte(`{"data":{"models":[{"model_name":"glm-5.2","avg_latency_ms":15860,"success_rate":100,"avg_tps":54.5}]}}`)
	models, err := decodePricingModels(body)
	if err != nil || len(models) != 1 {
		t.Fatalf("decode summary: %+v %v", models, err)
	}
	if models[0].Model != "glm-5.2" || models[0].Latency == nil || *models[0].Latency != 15860 || models[0].TPS == nil || *models[0].TPS != 54.5 {
		t.Fatalf("summary fields lost: %+v", models[0])
	}
}

func TestProbeAcceptsStringCatalogAndModelPathDetails(t *testing.T) {
	now := time.Unix(1786597326, 0).UTC()
	config := `{"catalogPath":"/selected","statusPath":"","detailPathTemplate":"/status/{model}?window=24h"}`
	responses := map[string][]byte{
		"https://example.test/selected":                  []byte(`{"data":["GLM-5.2","Kimi-K2.6"]}`),
		"https://example.test/status/GLM-5.2?window=24h": []byte(`{"data":{"model_name":"GLM-5.2","success_rate":96.63,"total_requests":6209,"success_count":6000,"failure_count":144,"empty_count":65,"slot_data":[{"start_time":1786593726,"end_time":1786597326,"success_rate":99.25,"total_requests":268,"success_count":266,"failure_count":2,"empty_count":0}]}}`),
	}
	probe := NewAPIProbeAdapter()
	collection, err := probe.Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: config}, fakeFetcher{responses: responses}, now)
	if err != nil || len(collection.Models) != 2 || collection.Models[0].RawName != "GLM-5.2" {
		t.Fatalf("string catalog: %+v %v", collection, err)
	}
	collection.Models = collection.Models[:1]
	if err := probe.CollectDetails(context.Background(), Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: config}, fakeFetcher{responses: responses}, &collection, []string{"GLM-5.2"}, now); err != nil {
		t.Fatal(err)
	}
	group := collection.Models[0].Groups[0]
	if group.Metrics.SuccessRatio == nil || *group.Metrics.SuccessRatio < 0.96629 || *group.Metrics.SuccessRatio > 0.96631 || len(group.Buckets) != 1 {
		t.Fatalf("probe detail lost: %+v", group)
	}
}

func TestProbeRejectsInvalidSchemaConfig(t *testing.T) {
	probe := NewAPIProbeAdapter()
	_, err := probe.Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"pageSize":0}`}, fakeFetcher{responses: map[string][]byte{}}, time.Now())
	if err == nil {
		t.Fatal("expected pageSize minimum validation error")
	}
}

func TestProbePricingCanBeAddedByConfiguration(t *testing.T) {
	probe := NewAPIProbeAdapter()
	config := `{"catalogPath":"/catalog","statusPath":"","detailPath":"","pricingAdapter":"newapi","pricingPath":"/pricing","pricingStatusPath":"/status"}`
	responses := map[string][]byte{
		"https://example.test/catalog": []byte(`{"data":["gpt-5.5"]}`),
		"https://example.test/pricing": []byte(`{"group_ratio":{"default":0.5},"data":[{"model_name":"gpt-5.5","quota_type":0,"model_ratio":2,"completion_ratio":3,"enable_groups":["default"]}]}`),
		"https://example.test/status":  []byte(`{"data":{"quota_per_unit":500000,"quota_display_type":"USD","custom_currency_symbol":"$"}}`),
	}
	collection, err := probe.Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: config}, fakeFetcher{responses: responses}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 1 || len(collection.Models[0].Groups) != 1 || !strings.Contains(string(collection.Models[0].Groups[0].Extension), `"inputPerMillion":2`) {
		t.Fatalf("configured probe pricing missing: %+v", collection.Models)
	}
}

func TestProbeSupportsEnhancementStatusProtocol(t *testing.T) {
	now := time.Unix(1786846070, 0).UTC()
	config := `{"catalogPath":"/api/enhancements/model-status/embed/status/all","statusPath":"","detailPath":"","detailPathTemplate":"/api/enhancements/model-status/embed/status/{model}?window=24h","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`
	responses := map[string][]byte{
		"https://example.test/api/enhancements/model-status/embed/status/all":                    []byte(`{"success":true,"data":[{"model_name":"gpt-5.6-sol","group":"default","success_rate":68.1,"current_status":"red"}]}`),
		"https://example.test/api/enhancements/model-status/embed/status/gpt-5.6-sol?window=24h": []byte(`{"success":true,"data":{"model_name":"gpt-5.6-sol","group":"default","success_rate":68.1,"total_requests":10,"success_count":7,"error_count":3,"slot_data":[{"start_time":1786842470,"end_time":1786846070,"success_rate":70,"total_requests":10,"success_count":7,"error_count":3}]}}`),
		"https://example.test/api/pricing":                                                       []byte(`{"group_ratio":{"default":1},"data":[{"model_name":"gpt-5.6-sol","quota_type":0,"model_ratio":2,"completion_ratio":3,"enable_groups":["default"]}]}`),
		"https://example.test/api/status":                                                        []byte(`{"data":{"quota_display_type":"USD","custom_currency_symbol":"$"}}`),
	}
	probe := NewAPIProbeAdapter()
	site := Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: config}
	collection, err := probe.Collect(context.Background(), site, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.CollectDetails(context.Background(), site, fakeFetcher{responses: responses}, &collection, []string{"gpt-5.6-sol"}, now); err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 1 || len(collection.Models[0].Groups) != 1 {
		t.Fatalf("enhancement status models = %+v", collection.Models)
	}
	group := collection.Models[0].Groups[0]
	if group.Metrics.RequestCount == nil || *group.Metrics.RequestCount != 10 || group.Metrics.FailureCount == nil || *group.Metrics.FailureCount != 3 || len(group.Buckets) != 1 {
		t.Fatalf("enhancement status metrics = %+v", group)
	}
	if !strings.Contains(string(group.Extension), `"groupMultiplier":1`) {
		t.Fatalf("enhancement status pricing missing: %s", group.Extension)
	}
}

func TestProbeUsesCrossOriginStatusAndProjectsMetricsOntoPricingGroups(t *testing.T) {
	now := time.Unix(1786846070, 0).UTC()
	config := `{"statusBaseUrl":"https://status.example.test","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`
	responses := map[string][]byte{
		"https://status.example.test/api/model-status/embed/config/selected":            []byte(`{"success":true,"data":["grok-4.5"]}`),
		"https://status.example.test/api/model-status/embed/status/batch?window=24h":    []byte(`{"success":true,"data":[]}`),
		"https://status.example.test/api/model-status/embed/status/grok-4.5?window=24h": []byte(`{"success":true,"data":{"model_name":"grok-4.5","success_rate":98.38,"total_requests":11700,"success_count":11511,"failure_count":38,"empty_count":151,"slot_data":[{"start_time":1786842470,"end_time":1786846070,"success_rate":98.4,"total_requests":500,"success_count":492,"failure_count":3,"empty_count":5}]}}`),
		"https://api.example.test/api/pricing":                                          []byte(`{"group_ratio":{"level1":1,"level3":2},"data":[{"model_name":"grok-4.5","quota_type":1,"model_price":0.001,"enable_groups":["level1","level3"]}]}`),
		"https://api.example.test/api/status":                                           []byte(`{"data":{"quota_display_type":"USD","custom_currency_symbol":"$"}}`),
	}
	probe := NewAPIProbeAdapter()
	site := Site{ID: 1, BaseURL: "https://api.example.test", ConfigJSON: config}
	collection, err := probe.Collect(context.Background(), site, fakeFetcher{responses: responses}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 1 || len(collection.Models[0].Groups) != 2 {
		t.Fatalf("probe pricing groups = %+v", collection.Models)
	}
	if err := probe.CollectDetails(context.Background(), site, fakeFetcher{responses: responses}, &collection, []string{"grok-4.5"}, now); err != nil {
		t.Fatal(err)
	}
	for _, group := range collection.Models[0].Groups {
		if group.RawName != "level1" && group.RawName != "level3" {
			t.Fatalf("unexpected projected group: %+v", group)
		}
		if group.Metrics.RequestCount == nil || *group.Metrics.RequestCount != 11700 || group.Metrics.SuccessRatio == nil || *group.Metrics.SuccessRatio < 0.9837 || len(group.Buckets) != 1 {
			t.Fatalf("monitor metrics not projected onto %q: %+v", group.RawName, group)
		}
		if !strings.Contains(string(group.Extension), `"fixedPerRequest"`) {
			t.Fatalf("price missing from %q: %s", group.RawName, group.Extension)
		}
	}
}

func TestCustomProbePreservesStaleSourceTimeAndDropsExpiredBars(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	lastSample := now.Add(-9 * 24 * time.Hour)
	body := []byte(fmt.Sprintf(`{"ok":true,"models":[{"model":"kimi-k3","total":1367,"ok":1353,"empty":14,"err429":0,"errOther":0,"successRate":99,"avgLatency":42,"health":"healthy","lastTs":%d,"bars":[{"time":%d,"status":"up","total":3,"ok":3},{"time":%d,"status":"unknown","total":2,"ok":2}]}]}`,
		lastSample.UnixMilli(), lastSample.Add(-10*time.Minute).UnixMilli(), lastSample.Add(-5*time.Minute).UnixMilli()))
	endpoint := "https://example.test/api/model-status?window=3600&recent=50"

	collection, err := CustomProbeAdapter().Collect(
		context.Background(),
		Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"statusPath":""}`},
		fakeFetcher{responses: map[string][]byte{endpoint: body}},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	group := collection.Models[0].Groups[0]
	if !group.ObservedAt.Equal(lastSample) {
		t.Fatalf("observed at = %s, want source last sample %s", group.ObservedAt, lastSample)
	}
	if group.ServiceState != domain.ServiceNoSamples {
		t.Fatalf("service state = %q, want latest source bar no_samples", group.ServiceState)
	}
	if len(group.Buckets) != 0 {
		t.Fatalf("expired buckets retained: %+v", group.Buckets)
	}
	if group.Metrics.SuccessRatio != nil || group.Metrics.RequestCount != nil || group.Metrics.AverageLatencyMS != nil {
		t.Fatalf("stale lifetime aggregate leaked into 24h metrics: %+v", group.Metrics)
	}
}

func TestCustomProbeUsesRecentBarsForCurrentStateAndMetrics(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	lastSample := now.Add(-2 * time.Minute)
	body := []byte(fmt.Sprintf(`{"ok":true,"models":[{"model":"active-model","total":999,"ok":999,"empty":0,"err429":0,"errOther":0,"successRate":100,"avgLatency":42,"health":"healthy","lastTs":%d,"bars":[{"time":%d,"status":"up","total":10,"ok":10},{"time":%d,"status":"up","total":3,"ok":3},{"time":%d,"status":"down","total":2,"ok":0}]}]}`,
		lastSample.UnixMilli(), now.Add(-30*time.Hour).UnixMilli(), now.Add(-10*time.Minute).UnixMilli(), now.Add(-5*time.Minute).UnixMilli()))
	endpoint := "https://example.test/api/model-status?window=3600&recent=50"

	collection, err := CustomProbeAdapter().Collect(
		context.Background(),
		Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"statusPath":""}`},
		fakeFetcher{responses: map[string][]byte{endpoint: body}},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	group := collection.Models[0].Groups[0]
	if !group.ObservedAt.Equal(lastSample) {
		t.Fatalf("observed at = %s, want %s", group.ObservedAt, lastSample)
	}
	if group.ServiceState != domain.ServiceFailed {
		t.Fatalf("service state = %q, want latest bar failed", group.ServiceState)
	}
	if len(group.Buckets) != 3 {
		t.Fatalf("recent retained buckets = %d, want 3", len(group.Buckets))
	}
	if group.Metrics.RequestCount == nil || *group.Metrics.RequestCount != 5 ||
		group.Metrics.SuccessCount == nil || *group.Metrics.SuccessCount != 3 ||
		group.Metrics.FailureCount == nil || *group.Metrics.FailureCount != 2 ||
		group.Metrics.SuccessRatio == nil || *group.Metrics.SuccessRatio != 0.6 {
		t.Fatalf("24h metrics = %+v, want 3/5 successes", group.Metrics)
	}
	if group.Metrics.AverageLatencyMS != nil {
		t.Fatalf("unscoped source latency leaked into 24h metrics: %+v", group.Metrics)
	}
	model := collection.Models[0]
	if !model.HistoryCoverageStart.Equal(now.Add(-30*time.Hour)) || !model.HistoryCoverageEnd.Equal(now) {
		t.Fatalf("bar-backed history coverage = (%s, %s), want earliest returned bar through collection", model.HistoryCoverageStart, model.HistoryCoverageEnd)
	}
}

func TestCustomProbeDoesNotDeclareCoverageWithoutBars(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"ok":true,"models":[{"model":"presence-only","total":0,"ok":0,"empty":0,"err429":0,"errOther":0,"successRate":0,"health":"healthy","bars":[]}]}`)
	endpoint := "https://example.test/api/model-status?window=3600&recent=50"

	collection, err := CustomProbeAdapter().Collect(
		context.Background(),
		Site{ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"statusPath":""}`},
		fakeFetcher{responses: map[string][]byte{endpoint: body}},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	model := collection.Models[0]
	if !model.HistoryCoverageStart.IsZero() || !model.HistoryCoverageEnd.IsZero() {
		t.Fatalf("barless probe declared history coverage: (%s, %s)", model.HistoryCoverageStart, model.HistoryCoverageEnd)
	}
}

func TestModelMarketParsesSub2APIItems(t *testing.T) {
	body := []byte(`{"items":[{"key":"gpt-5.6-sol:9","model":"gpt-5.6-sol","channel":{"id":9,"name":"premium","type":"openai"},"health":{"current":{"success_rate":92,"latency_ms":820}}}],"page":1,"page_size":100,"total":1}`)
	fetcher := fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/v1/model-market?group_by=model&sort_by=model&sort_order=asc&page=1&page_size=100&range=24h": body,
	}}
	collection, err := ModelMarketAdapter().Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fetcher, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 1 || collection.Models[0].RawName != "gpt-5.6-sol" || collection.Models[0].Provider != "openai" || collection.Models[0].Groups[0].RawName != "premium" {
		t.Fatalf("unexpected Sub2API model: %+v", collection.Models)
	}
	if ratio := collection.Models[0].Groups[0].Metrics.SuccessRatio; ratio == nil || *ratio != 0.92 {
		t.Fatalf("unexpected Sub2API health metrics: %+v", collection.Models[0].Groups[0].Metrics)
	}
}

func TestModelMarketCollectsEmbeddedHistory(t *testing.T) {
	now := time.Date(2026, time.August, 16, 3, 0, 0, 0, time.UTC)
	body := []byte(`{"data":{"items":[
		{"model":"deepseek-v4-pro","channel":{"name":"premium","type":"openai"},"health":{"current":{"bucket_start":"2026-08-16T02:00:00Z","bucket_end":"2026-08-16T03:00:00Z","sample_count":0,"success_rate":null,"is_current":true,"is_complete":false},"buckets":[{"bucket_start":"2026-08-16T01:00:00Z","bucket_end":"2026-08-16T02:00:00Z","success_count":0,"failure_count":2,"sample_count":2,"success_rate":0,"is_complete":true},{"bucket_start":"2026-08-16T02:00:00Z","bucket_end":"2026-08-16T03:00:00Z","success_count":0,"failure_count":0,"sample_count":0,"success_rate":null,"is_current":true,"is_complete":false}]}},
		{"model":"deepseek-v4-pro","channel":{"name":"discount","type":"openai"},"health":{"current":{"bucket_start":"2026-08-16T02:00:00Z","bucket_end":"2026-08-16T03:00:00Z","sample_count":1,"success_rate":100,"is_current":true,"is_complete":false},"buckets":[{"bucket_start":"2026-08-16T02:00:00Z","bucket_end":"2026-08-16T03:00:00Z","success_count":1,"failure_count":0,"sample_count":1,"success_rate":100,"is_current":true,"is_complete":false}]}}
	]}}`)
	fetcher := fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/v1/model-market?group_by=model&sort_by=model&sort_order=asc&page=1&page_size=100&range=24h": body,
	}}

	collection, err := ModelMarketAdapter().Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fetcher, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 1 || len(collection.Models[0].Groups) != 2 {
		t.Fatalf("unexpected model-market groups: %+v", collection.Models)
	}
	groups := make(map[string]domain.GroupObservation)
	for _, group := range collection.Models[0].Groups {
		groups[group.RawName] = group
	}
	premium := groups["premium"]
	if premium.ServiceState != domain.ServiceFailed || !premium.ObservedAt.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("incomplete empty bucket replaced current state: %+v", premium)
	}
	if len(premium.Buckets) != 2 || premium.Buckets[0].Metrics.SuccessRatio == nil || *premium.Buckets[0].Metrics.SuccessRatio != 0 {
		t.Fatalf("premium history lost: %+v", premium.Buckets)
	}
	if premium.Metrics.RequestCount == nil || *premium.Metrics.RequestCount != 2 ||
		premium.Metrics.SuccessCount == nil || *premium.Metrics.SuccessCount != 0 ||
		premium.Metrics.FailureCount == nil || *premium.Metrics.FailureCount != 2 ||
		premium.Metrics.SuccessRatio == nil || *premium.Metrics.SuccessRatio != 0 {
		t.Fatalf("premium 24h metrics = %+v", premium.Metrics)
	}
	discount := groups["discount"]
	if discount.ServiceState != domain.ServiceHealthy || len(discount.Buckets) != 1 ||
		discount.Metrics.RequestCount == nil || *discount.Metrics.RequestCount != 1 {
		t.Fatalf("discount history lost: %+v", discount)
	}
	model := collection.Models[0]
	if !model.HistoryCoverageStart.Equal(now.Add(-24*time.Hour)) || !model.HistoryCoverageEnd.Equal(now) {
		t.Fatalf("model-market history coverage = (%s, %s)", model.HistoryCoverageStart, model.HistoryCoverageEnd)
	}
}

func TestModelMarketWeightsHistoryRatiosWhenCountsAreUnavailable(t *testing.T) {
	now := time.Date(2026, time.August, 16, 3, 0, 0, 0, time.UTC)
	body := []byte(`{"items":[{"model":"deepseek-v4-pro","channel":{"name":"premium"},"health":{"buckets":[{"bucket_start":"2026-08-16T01:00:00Z","bucket_end":"2026-08-16T02:00:00Z","sample_count":1,"success_rate":100},{"bucket_start":"2026-08-16T02:00:00Z","bucket_end":"2026-08-16T03:00:00Z","sample_count":3,"success_rate":0}]}}]}`)
	fetcher := fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/v1/model-market?group_by=model&sort_by=model&sort_order=asc&page=1&page_size=100&range=24h": body,
	}}

	collection, err := ModelMarketAdapter().Collect(context.Background(), Site{ID: 1, BaseURL: "https://example.test"}, fetcher, now)
	if err != nil {
		t.Fatal(err)
	}
	metrics := collection.Models[0].Groups[0].Metrics
	if metrics.RequestCount == nil || *metrics.RequestCount != 4 || metrics.SuccessCount != nil ||
		metrics.SuccessRatio == nil || *metrics.SuccessRatio != 0.25 {
		t.Fatalf("weighted 24h metrics = %+v, want 25%% across 4 requests", metrics)
	}
}

func TestModelMarketAttachesPricingFromCatalogResponse(t *testing.T) {
	body := []byte(`{"data":{"items":[{"model":"gpt-5.6-sol","channel":{"name":"premium","type":"openai"},"pricing":{"billing_mode":"token","currency":"USD","input_price":0.000002,"output_price":0.000006},"rates":{"effective_text_multiplier":0.5},"health":{"current":{"success_rate":92}}}]}}`)
	fetcher := fakeFetcher{responses: map[string][]byte{
		"https://example.test/api/v1/model-market?group_by=model&sort_by=model&sort_order=asc&page=1&page_size=100&range=24h": body,
	}}
	collection, err := ModelMarketAdapter().Collect(context.Background(), Site{
		ID: 1, BaseURL: "https://example.test", ConfigJSON: `{"pricingAdapter":"model-market"}`,
	}, fetcher, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	group := collection.Models[0].Groups[0]
	if !strings.Contains(string(group.Extension), `"inputPerMillion":1`) || !strings.Contains(string(group.Extension), `"groupMultiplier":0.5`) {
		t.Fatalf("model-market price missing: %s", group.Extension)
	}
}

func TestModelMarketAttachesPricingAcrossCatalogPages(t *testing.T) {
	firstURL := "https://example.test/api/v1/model-market?group_by=model&sort_by=model&sort_order=asc&page=1&page_size=1"
	secondURL := "https://example.test/api/v1/model-market?group_by=model&page=2&page_size=1&sort_by=model&sort_order=asc"
	thirdURL := "https://example.test/api/v1/model-market?group_by=model&page=3&page_size=1&sort_by=model&sort_order=asc"
	fetcher := fakeFetcher{responses: map[string][]byte{
		firstURL:  []byte(`{"data":{"items":[{"model":"first-model","channel":{"name":"default"},"pricing":{"input_price":0.000001,"output_price":0.000002}}]}}`),
		secondURL: []byte(`{"data":{"items":[{"model":"second-model","channel":{"name":"default"},"pricing":{"input_price":0.000003,"output_price":0.000004}}]}}`),
		thirdURL:  []byte(`{"data":{"items":[]}}`),
	}}
	collection, err := ModelMarketAdapter().Collect(context.Background(), Site{
		ID: 1, BaseURL: "https://example.test",
		ConfigJSON: `{"catalogPath":"/api/v1/model-market?group_by=model&sort_by=model&sort_order=asc&page=1&page_size=1","pageSize":1,"pricingAdapter":"model-market"}`,
	}, fetcher, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Models) != 2 || !strings.Contains(string(collection.Models[1].Groups[0].Extension), `"inputPerMillion":3`) {
		t.Fatalf("later-page pricing missing: %+v", collection.Models)
	}
}

func TestModelMarketDoesNotReuseCatalogAsDetailEndpoint(t *testing.T) {
	collection := domain.Collection{Models: []domain.ModelObservation{{RawName: "gpt-5.6-sol"}}}
	err := ModelMarketAdapter().CollectDetails(
		context.Background(),
		Site{ID: 1, BaseURL: "https://example.test"},
		fakeFetcher{responses: map[string][]byte{}},
		&collection,
		[]string{"gpt-5.6-sol"},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("model market without a detail endpoint attempted a request: %v", err)
	}
}
