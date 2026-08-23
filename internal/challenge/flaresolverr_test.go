package challenge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFlareSolverrLoopbackAndCookieExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload requestPayload
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Cmd != "request.get" {
			t.Fatalf("bad request: %+v err=%v", payload, err)
		}
		if payload.MaxTimeout != 120000 {
			t.Fatalf("unexpected max timeout: %d", payload.MaxTimeout)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": "ok", "solution": map[string]any{"response": "<html>", "userAgent": "ua", "cookies": []map[string]string{{"name": "cf_clearance", "value": "x"}}}})
	}))
	defer server.Close()
	if _, err := NewFlareSolverr("https://example.test:8191"); err == nil {
		t.Fatal("non-loopback endpoint should be rejected")
	}
	solver, err := NewFlareSolverr(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if solver.Client.Timeout != 130*time.Second {
		t.Fatalf("unexpected client timeout: %s", solver.Client.Timeout)
	}
	solver.Client = server.Client()
	result, err := solver.Solve(context.Background(), "https://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	if result.UserAgent != "ua" || len(result.Cookies) != 1 || result.Cookies[0].Name != "cf_clearance" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
