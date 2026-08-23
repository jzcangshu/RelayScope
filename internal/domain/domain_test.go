package domain

import (
	"testing"
	"time"
)

func TestCollectionValidate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	collection := Collection{
		SiteID:      1,
		ObservedAt:  now,
		CollectedAt: now,
		Models: []ModelObservation{{
			RawName: "gpt-5.6-sol",
			Groups: []GroupObservation{{
				RawName:      "free group",
				ServiceState: ServiceHealthy,
				Buckets: []TimeBucket{{
					Start:      now.Add(-5 * time.Minute),
					End:        now,
					Resolution: 5 * time.Minute,
				}},
			}},
		}},
	}

	if err := collection.Validate(); err != nil {
		t.Fatalf("validate collection: %v", err)
	}
}

func TestCollectionValidateRejectsDuplicateRawIdentity(t *testing.T) {
	t.Parallel()

	now := time.Now()
	collection := Collection{
		SiteID:      1,
		ObservedAt:  now,
		CollectedAt: now,
		Models: []ModelObservation{
			{RawName: "same", Groups: []GroupObservation{{RawName: "group", ServiceState: ServiceUnknown}}},
			{RawName: "same", Groups: []GroupObservation{{RawName: "other", ServiceState: ServiceUnknown}}},
		},
	}

	if err := collection.Validate(); err == nil {
		t.Fatal("expected duplicate model error")
	}
}

func TestCollectionValidateRejectsPartialHistoryCoverage(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	collection := Collection{
		SiteID: 1, ObservedAt: now, CollectedAt: now,
		Models: []ModelObservation{{
			RawName: "gpt-5.6-sol", HistoryCoverageStart: now.Add(-24 * time.Hour),
			Groups: []GroupObservation{{RawName: "default", ServiceState: ServiceNoSamples}},
		}},
	}

	if err := collection.Validate(); err == nil {
		t.Fatal("expected partial history coverage error")
	}
}

func TestCollectionValidateRejectsReversedHistoryCoverage(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	collection := Collection{
		SiteID: 1, ObservedAt: now, CollectedAt: now,
		Models: []ModelObservation{{
			RawName: "gpt-5.6-sol", HistoryCoverageStart: now, HistoryCoverageEnd: now.Add(-time.Hour),
			Groups: []GroupObservation{{RawName: "default", ServiceState: ServiceNoSamples}},
		}},
	}

	if err := collection.Validate(); err == nil {
		t.Fatal("expected reversed history coverage error")
	}
}
