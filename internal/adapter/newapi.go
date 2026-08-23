package adapter

import (
	"encoding/json"

	"relaypulse/internal/pricing"
)

type NewAPIConfig struct {
	PricingPath       string `json:"pricingPath"`
	SummaryPath       string `json:"summaryPath"`
	DetailPath        string `json:"detailPath"`
	WindowHours       int    `json:"windowHours"`
	CatalogPageSize   int    `json:"catalogPageSize"`
	SkipDetails       bool   `json:"skipDetails"`
	PricingAdapter    string `json:"pricingAdapter"`
	PricingStatusPath string `json:"pricingStatusPath"`
	AvailabilityMode  string `json:"availabilityMode"`
	PricingNeedsLogin bool   `json:"pricingRequiresSession"`
}

type NewAPIAdapter struct {
	PricingRegistry *pricing.Registry
}

func (NewAPIAdapter) Key() string         { return "newapi-pricing" }
func (NewAPIAdapter) DisplayName() string { return "NewAPI 模型广场" }
func (NewAPIAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pricingPath":{"type":"string","default":"/api/pricing"},"summaryPath":{"type":"string","default":"/api/perf-metrics/summary"},"detailPath":{"type":"string","default":"/api/perf-metrics"},"windowHours":{"type":"integer","minimum":1,"maximum":72,"default":24},"catalogPageSize":{"type":"integer","minimum":1,"maximum":1000,"default":1000},"skipDetails":{"type":"boolean","default":false},"pricingAdapter":{"type":"string","default":"newapi"},"pricingStatusPath":{"type":"string","default":"/api/status"},"availabilityMode":{"type":"string","enum":["metrics","presence"]},"pricingRequiresSession":{"type":"boolean"}}}`)
}

type pricingModel struct {
	Model          string   `json:"model"`
	Provider       string   `json:"provider"`
	Group          string   `json:"group"`
	Groups         []string `json:"groups"`
	Channel        string   `json:"channel"`
	Status         string   `json:"status"`
	SuccessRate    *float64 `json:"success_rate"`
	Latency        *float64 `json:"latency"`
	TPS            *float64 `json:"tps"`
	Buckets        []detailBucket
	HistoryPresent bool
}

type summaryResponse struct {
	Data []summaryModel `json:"data"`
}

type summaryModel struct {
	Model       string   `json:"model"`
	Provider    string   `json:"provider"`
	Group       string   `json:"group"`
	SuccessRate *float64 `json:"success_rate"`
	Latency     *float64 `json:"latency"`
	TPS         *float64 `json:"tps"`
	Requests    *int64   `json:"requests"`
	Success     *int64   `json:"success"`
	Failure     *int64   `json:"failure"`
}

type detailBucket struct {
	Aggregate    bool
	Complete     *bool
	Timestamp    int64    `json:"timestamp"`
	EndTimestamp int64    `json:"end_timestamp"`
	Time         string   `json:"time"`
	EndTime      string   `json:"end_time"`
	Group        string   `json:"group"`
	SuccessRate  *float64 `json:"success_rate"`
	Latency      *float64 `json:"latency"`
	TTFT         *float64 `json:"ttft"`
	TPS          *float64 `json:"tps"`
	Requests     *int64   `json:"requests"`
	Success      *int64   `json:"success"`
	Failure      *int64   `json:"failure"`
	Empty        *int64   `json:"empty"`
}
