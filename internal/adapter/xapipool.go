package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"relaypulse/internal/domain"
)

// XAPIPoolAdapter reads X-API's public text-pool request history. The source
// exposes two confirmed text models and now includes a separate heatmap row
// for each model in addition to the all-site aggregate row.
type XAPIPoolAdapter struct{}

func (XAPIPoolAdapter) Key() string         { return "xapi-pool" }
func (XAPIPoolAdapter) DisplayName() string { return "X-API Pool" }
func (XAPIPoolAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"heatmapPath":{"type":"string","default":"/api/pool/requests/heatmap?channel=text"}}}`)
}

type xAPIPoolConfig struct {
	HeatmapPath string `json:"heatmapPath"`
}

type xAPIHeatmapResponse struct {
	Cells  []xAPIHeatmapCell     `json:"cells"`
	Models []xAPIHeatmapModelRow `json:"models"`
}

type xAPIHeatmapModelRow struct {
	Model string            `json:"model"`
	Cells []xAPIHeatmapCell `json:"cells"`
}

type xAPIHeatmapCell struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	OK      int64     `json:"ok"`
	Limited int64     `json:"limited"`
	Error   int64     `json:"error"`
	Total   int64     `json:"total"`
}

func (adapter XAPIPoolAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	config := xAPIPoolConfig{HeatmapPath: "/api/pool/requests/heatmap?channel=text"}
	if strings.TrimSpace(site.ConfigJSON) != "" {
		if err := json.Unmarshal([]byte(site.ConfigJSON), &config); err != nil {
			return domain.Collection{}, fmt.Errorf("decode X-API pool config: %w", err)
		}
	}
	if strings.TrimSpace(config.HeatmapPath) == "" {
		return domain.Collection{}, fmt.Errorf("X-API pool paths are required")
	}

	heatmapURL, err := resolveSiteURL(site.BaseURL, config.HeatmapPath)
	if err != nil {
		return domain.Collection{}, err
	}

	var heatmap xAPIHeatmapResponse
	if err := fetcher.GetJSON(ctx, heatmapURL, &heatmap); err != nil {
		return domain.Collection{}, err
	}

	allSiteBuckets := xAPIRequestBuckets(heatmap.Cells, now.UTC())
	modelCells := make(map[string][]xAPIHeatmapCell, len(heatmap.Models))
	for _, model := range heatmap.Models {
		if strings.TrimSpace(model.Model) != "" {
			modelCells[model.Model] = model.Cells
		}
	}

	collection := domain.Collection{SiteID: site.ID, CollectedAt: now.UTC(), CatalogComplete: true}
	for _, modelName := range []string{"grok-4.5", "grok-4.6"} {
		modelBuckets := allSiteBuckets
		if len(heatmap.Models) > 0 {
			modelBuckets = xAPIRequestBuckets(modelCells[modelName], now.UTC())
		}
		observedAt, state := xAPIModelRequestState(modelBuckets, allSiteBuckets, now.UTC())
		if collection.ObservedAt.IsZero() || observedAt.After(collection.ObservedAt) {
			collection.ObservedAt = observedAt
		}
		group := domain.GroupObservation{RawName: "default", ServiceState: state, ObservedAt: observedAt, Buckets: append([]domain.TimeBucket(nil), modelBuckets...)}
		model := domain.ModelObservation{RawName: modelName, Provider: "xAI", Groups: []domain.GroupObservation{group}}
		if len(modelBuckets) > 0 {
			model.HistoryCoverageStart = modelBuckets[0].Start
			model.HistoryCoverageEnd = modelBuckets[len(modelBuckets)-1].End
		}
		collection.Models = append(collection.Models, model)
		collection.CatalogRawNames = append(collection.CatalogRawNames, modelName)
	}
	return collection, nil
}

func xAPILatestRequestState(buckets []domain.TimeBucket, fallback time.Time) (time.Time, domain.ServiceState) {
	if len(buckets) == 0 {
		return fallback, domain.ServiceNoSamples
	}
	latest := buckets[len(buckets)-1]
	return latest.End, serviceState("", latest.Metrics.SuccessRatio)
}

func xAPIModelRequestState(modelBuckets, allSiteBuckets []domain.TimeBucket, fallback time.Time) (time.Time, domain.ServiceState) {
	observedAt, state := xAPILatestRequestState(modelBuckets, fallback)
	allObservedAt, allSiteState := xAPILatestRequestState(allSiteBuckets, fallback)
	if allSiteState == domain.ServiceFailed && !xAPICurrentIntervalHasData(modelBuckets, allSiteBuckets) {
		if allObservedAt.After(observedAt) {
			observedAt = allObservedAt
		}
		return observedAt, domain.ServiceFailed
	}
	return observedAt, state
}

func xAPICurrentIntervalHasData(modelBuckets, allSiteBuckets []domain.TimeBucket) bool {
	if len(modelBuckets) == 0 || len(allSiteBuckets) == 0 {
		return false
	}
	modelLatest := modelBuckets[len(modelBuckets)-1]
	allSiteLatest := allSiteBuckets[len(allSiteBuckets)-1]
	return modelLatest.End.Equal(allSiteLatest.End) && modelLatest.Metrics.SuccessRatio != nil
}

func xAPIRequestBuckets(cells []xAPIHeatmapCell, now time.Time) []domain.TimeBucket {
	valid := make([]xAPIHeatmapCell, 0, len(cells))
	for _, cell := range cells {
		if cell.Start.IsZero() || cell.End.IsZero() || !cell.End.After(cell.Start) || cell.Start.After(now) {
			continue
		}
		if cell.End.After(now) {
			cell.End = now
		}
		if !cell.End.After(cell.Start) || cell.Total < 0 || cell.OK < 0 || cell.Limited < 0 || cell.Error < 0 {
			continue
		}
		valid = append(valid, cell)
	}
	sort.SliceStable(valid, func(left, right int) bool { return valid[left].Start.Before(valid[right].Start) })
	buckets := make([]domain.TimeBucket, 0, len(valid))
	for _, cell := range valid {
		requestCount, successCount, failureCount, emptyCount := cell.Total, cell.OK, cell.Error, cell.Limited
		metrics := domain.Metrics{RequestCount: &requestCount, SuccessCount: &successCount, FailureCount: &failureCount, EmptyCount: &emptyCount}
		if cell.Total > 0 {
			ratio := float64(cell.OK) / float64(cell.Total)
			metrics.SuccessRatio = &ratio
		}
		buckets = append(buckets, domain.TimeBucket{Start: cell.Start.UTC(), End: cell.End.UTC(), Resolution: cell.End.Sub(cell.Start), Metrics: metrics})
	}
	return buckets
}
