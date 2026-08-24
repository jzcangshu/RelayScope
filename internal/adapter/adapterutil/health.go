// Package adapterutil provides shared helpers used by adapter implementations
// so that thresholds, timestamp parsing, and ratio normalization are defined
// in one place.
package adapterutil

import (
	"relayscope/internal/domain"
)

// Health thresholds for converting a success ratio into a ServiceState.
// These mirror the CASE expressions in internal/store/query.go; changing
// them requires updating the SQL queries as well.
const (
	HealthyRatio  = 0.85
	DegradedRatio = 0.50
)

// RatioToServiceState maps a success ratio (0-1, or >1 meaning percent)
// to a ServiceState using the shared thresholds. A nil ratio means no
// samples were observed.
func RatioToServiceState(ratio *float64) domain.ServiceState {
	if ratio == nil {
		return domain.ServiceNoSamples
	}
	normalized := NormalizeRatio(ratio)
	if *normalized >= HealthyRatio {
		return domain.ServiceHealthy
	}
	if *normalized >= DegradedRatio {
		return domain.ServiceDegraded
	}
	return domain.ServiceFailed
}

// NormalizeRatio converts percentage-style values to ratios. If the input is
// already <= 1 it is returned as-is; otherwise it is assumed to be a
// percentage (e.g. 85 meaning 0.85). It intentionally does not clamp values.
func NormalizeRatio(value *float64) *float64 {
	if value == nil {
		return nil
	}
	if *value <= 1 {
		return value
	}
	normalized := *value / 100
	return &normalized
}
