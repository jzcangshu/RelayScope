package adapterutil

import (
	"testing"
	"time"

	"relayscope/internal/domain"
)

func TestRatioToServiceState(t *testing.T) {
	tests := []struct {
		name  string
		ratio *float64
		want  domain.ServiceState
	}{
		{"nil", nil, domain.ServiceNoSamples},
		{"healthy", p(0.9), domain.ServiceHealthy},
		{"healthy boundary", p(0.85), domain.ServiceHealthy},
		{"degraded", p(0.6), domain.ServiceDegraded},
		{"degraded boundary", p(0.50), domain.ServiceDegraded},
		{"failed", p(0.3), domain.ServiceFailed},
		{"percent healthy", p(90), domain.ServiceHealthy},
		{"percent degraded", p(60), domain.ServiceDegraded},
		{"percent failed", p(30), domain.ServiceFailed},
		{"zero", p(0), domain.ServiceFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RatioToServiceState(tt.ratio); got != tt.want {
				t.Fatalf("RatioToServiceState(%v) = %v, want %v", tt.ratio, got, tt.want)
			}
		})
	}
}

func TestParseFlexibleTime(t *testing.T) {
	// Unix seconds
	got := ParseFlexibleTime(1700000000)
	if !got.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("seconds: got %v", got)
	}
	// Unix milliseconds
	got = ParseFlexibleTime(1700000000000)
	if !got.Equal(time.UnixMilli(1700000000000).UTC()) {
		t.Fatalf("millis: got %v", got)
	}
	// Zero / negative
	if ParseFlexibleTime(0) != (time.Time{}) {
		t.Fatal("zero should produce zero time")
	}
	if ParseFlexibleTime(-1) != (time.Time{}) {
		t.Fatal("negative should produce zero time")
	}
}

func p(v float64) *float64 { return &v }
