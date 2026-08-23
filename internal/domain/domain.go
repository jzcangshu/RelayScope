package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

type ServiceState string

const (
	ServiceUnknown   ServiceState = "unknown"
	ServiceHealthy   ServiceState = "healthy"
	ServiceDegraded  ServiceState = "degraded"
	ServiceFailed    ServiceState = "failed"
	ServiceNoSamples ServiceState = "no_samples"
	ServiceRemoved   ServiceState = "removed"
)

func (state ServiceState) Valid() bool {
	switch state {
	case ServiceUnknown, ServiceHealthy, ServiceDegraded, ServiceFailed, ServiceNoSamples, ServiceRemoved:
		return true
	default:
		return false
	}
}

type AcquisitionState string

const (
	AcquisitionFresh            AcquisitionState = "fresh"
	AcquisitionStale            AcquisitionState = "stale"
	AcquisitionCollecting       AcquisitionState = "collecting"
	AcquisitionCollectionFailed AcquisitionState = "collection_failed"
	AcquisitionLoginExpired     AcquisitionState = "login_expired"
	AcquisitionChallengePending AcquisitionState = "challenge_pending"
	AcquisitionChallengeFailed  AcquisitionState = "challenge_failed"
)

func (state AcquisitionState) Valid() bool {
	switch state {
	case AcquisitionFresh, AcquisitionStale, AcquisitionCollecting, AcquisitionCollectionFailed,
		AcquisitionLoginExpired, AcquisitionChallengePending, AcquisitionChallengeFailed:
		return true
	default:
		return false
	}
}

type Metrics struct {
	RequestCount     *int64   `json:"requestCount,omitempty"`
	SuccessCount     *int64   `json:"successCount,omitempty"`
	FailureCount     *int64   `json:"failureCount,omitempty"`
	EmptyCount       *int64   `json:"emptyCount,omitempty"`
	SuccessRatio     *float64 `json:"successRatio,omitempty"`
	AverageLatencyMS *float64 `json:"averageLatencyMs,omitempty"`
	FirstTokenMS     *float64 `json:"firstTokenMs,omitempty"`
	TokensPerSecond  *float64 `json:"tokensPerSecond,omitempty"`
}

type TimeBucket struct {
	Start      time.Time
	End        time.Time
	Resolution time.Duration
	Metrics    Metrics
}

type GroupObservation struct {
	RawName      string
	ServiceState ServiceState
	ObservedAt   time.Time
	Metrics      Metrics
	Buckets      []TimeBucket
	Extension    json.RawMessage
}

type ModelObservation struct {
	RawName              string
	Provider             string
	HistoryCoverageStart time.Time
	HistoryCoverageEnd   time.Time
	Groups               []GroupObservation
	Extension            json.RawMessage
}

type CollectionIssue struct {
	Code    string
	Scope   string
	Message string
}

type Collection struct {
	SiteID              int64
	RunID               int64
	ObservedAt          time.Time
	CollectedAt         time.Time
	CatalogComplete     bool
	CatalogRawNames     []string
	MissingCatalogState ServiceState
	Models              []ModelObservation
	Issues              []CollectionIssue
}

func (collection Collection) Validate() error {
	if collection.SiteID <= 0 {
		return fmt.Errorf("site ID must be positive")
	}
	if collection.ObservedAt.IsZero() || collection.CollectedAt.IsZero() {
		return fmt.Errorf("observation and collection times are required")
	}
	if collection.MissingCatalogState != "" {
		if !collection.CatalogComplete {
			return fmt.Errorf("missing catalog state requires a complete catalog")
		}
		if !collection.MissingCatalogState.Valid() || collection.MissingCatalogState == ServiceRemoved {
			return fmt.Errorf("invalid missing catalog state %q", collection.MissingCatalogState)
		}
	}
	seenModels := make(map[string]struct{}, len(collection.Models))
	seenCatalog := make(map[string]struct{}, len(collection.CatalogRawNames))
	for _, rawName := range collection.CatalogRawNames {
		if rawName == "" {
			return fmt.Errorf("catalog contains empty raw name")
		}
		if _, exists := seenCatalog[rawName]; exists {
			return fmt.Errorf("catalog contains duplicate raw name %q", rawName)
		}
		seenCatalog[rawName] = struct{}{}
	}
	for modelIndex, model := range collection.Models {
		if model.RawName == "" {
			return fmt.Errorf("model %d raw name is required", modelIndex)
		}
		if _, exists := seenModels[model.RawName]; exists {
			return fmt.Errorf("duplicate model raw name %q", model.RawName)
		}
		seenModels[model.RawName] = struct{}{}
		coverageStartSet := !model.HistoryCoverageStart.IsZero()
		coverageEndSet := !model.HistoryCoverageEnd.IsZero()
		if coverageStartSet != coverageEndSet || (coverageStartSet && model.HistoryCoverageEnd.Before(model.HistoryCoverageStart)) {
			return fmt.Errorf("model %q has invalid history coverage", model.RawName)
		}

		seenGroups := make(map[string]struct{}, len(model.Groups))
		for groupIndex, group := range model.Groups {
			if group.RawName == "" {
				return fmt.Errorf("model %q group %d raw name is required", model.RawName, groupIndex)
			}
			if !group.ServiceState.Valid() {
				return fmt.Errorf("model %q group %q has invalid service state %q", model.RawName, group.RawName, group.ServiceState)
			}
			if _, exists := seenGroups[group.RawName]; exists {
				return fmt.Errorf("model %q has duplicate group %q", model.RawName, group.RawName)
			}
			seenGroups[group.RawName] = struct{}{}

			for _, bucket := range group.Buckets {
				if bucket.Start.IsZero() || bucket.End.Before(bucket.Start) || bucket.Resolution <= 0 {
					return fmt.Errorf("model %q group %q has invalid time bucket", model.RawName, group.RawName)
				}
			}
		}
	}
	return nil
}
