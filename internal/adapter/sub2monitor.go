package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"relaypulse/internal/domain"
)

// Sub2MonitorAdapter reads the authenticated, user-facing channel monitor
// summary from Sub2API. The endpoint reports current status and a bounded
// timeline; it does not expose token prices, so no synthetic pricing is added.
type Sub2MonitorAdapter struct{}

func (Sub2MonitorAdapter) Key() string         { return "sub2api-monitor" }
func (Sub2MonitorAdapter) DisplayName() string { return "Sub2API 渠道监控" }
func (Sub2MonitorAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"monitorPath":{"type":"string","default":"/api/v1/channel-monitors"}}}`)
}

type sub2MonitorConfig struct {
	MonitorPath string `json:"monitorPath"`
}

type sub2MonitorEnvelope struct {
	Code    int             `json:"code"`
	Data    sub2MonitorData `json:"data"`
	Message string          `json:"message"`
}

type sub2MonitorData struct {
	Items []sub2MonitorItem `json:"items"`
}

type sub2MonitorItem struct {
	Provider         string              `json:"provider"`
	GroupName        string              `json:"group_name"`
	PrimaryModel     string              `json:"primary_model"`
	PrimaryStatus    string              `json:"primary_status"`
	PrimaryLatencyMS *int                `json:"primary_latency_ms"`
	Timeline         []sub2TimelinePoint `json:"timeline"`
	ExtraModels      []sub2ExtraModel    `json:"extra_models"`
}

type sub2ExtraModel struct {
	Model     string `json:"model"`
	Status    string `json:"status"`
	LatencyMS *int   `json:"latency_ms"`
}

type sub2TimelinePoint struct {
	Status    string `json:"status"`
	LatencyMS *int   `json:"latency_ms"`
	CheckedAt string `json:"checked_at"`
}

func (Sub2MonitorAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	config := sub2MonitorConfig{MonitorPath: "/api/v1/channel-monitors"}
	if site.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(site.ConfigJSON), &config); err != nil {
			return domain.Collection{}, fmt.Errorf("decode sub2api-monitor config: %w", err)
		}
	}
	if strings.TrimSpace(config.MonitorPath) == "" {
		config.MonitorPath = "/api/v1/channel-monitors"
	}
	endpoint, err := resolveSiteURL(site.BaseURL, config.MonitorPath)
	if err != nil {
		return domain.Collection{}, err
	}
	body, _, err := fetcher.GetBytes(ctx, endpoint)
	if err != nil {
		return domain.Collection{}, err
	}
	var response sub2MonitorEnvelope
	if err := json.Unmarshal(body, &response); err != nil {
		return domain.Collection{}, fmt.Errorf("decode sub2api-monitor response: %w", err)
	}
	if response.Code != 0 {
		message := strings.TrimSpace(response.Message)
		if message == "" {
			message = fmt.Sprintf("sub2api-monitor returned code %d", response.Code)
		}
		return domain.Collection{}, fmt.Errorf("%s", message)
	}
	if len(response.Data.Items) == 0 {
		return domain.Collection{}, fmt.Errorf("sub2api-monitor response contained no monitors")
	}

	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true}
	modelIndexes := make(map[string]int)
	for _, item := range response.Data.Items {
		groupName := strings.TrimSpace(item.GroupName)
		if groupName == "" {
			groupName = "default"
		}
		observedAt := latestSub2Timeline(item.Timeline)
		if observedAt.IsZero() {
			observedAt = now.UTC()
		}
		addSub2Observation(&collection, modelIndexes, strings.TrimSpace(item.PrimaryModel), item.Provider, groupName, item.PrimaryStatus, item.PrimaryLatencyMS, observedAt)
		for _, extra := range item.ExtraModels {
			addSub2Observation(&collection, modelIndexes, strings.TrimSpace(extra.Model), item.Provider, groupName, extra.Status, extra.LatencyMS, observedAt)
		}
	}
	if len(collection.Models) == 0 {
		return domain.Collection{}, fmt.Errorf("sub2api-monitor response contained no valid models")
	}
	collection.CatalogRawNames = make([]string, 0, len(collection.Models))
	for _, model := range collection.Models {
		collection.CatalogRawNames = append(collection.CatalogRawNames, model.RawName)
	}
	return collection, nil
}

func addSub2Observation(collection *domain.Collection, indexes map[string]int, modelName, provider, groupName, status string, latencyMS *int, observedAt time.Time) {
	if modelName == "" {
		return
	}
	index, exists := indexes[modelName]
	if !exists {
		index = len(collection.Models)
		indexes[modelName] = index
		collection.Models = append(collection.Models, domain.ModelObservation{RawName: modelName, Provider: strings.TrimSpace(provider)})
	}
	model := &collection.Models[index]
	if model.Provider == "" {
		model.Provider = strings.TrimSpace(provider)
	}
	for groupIndex := range model.Groups {
		if model.Groups[groupIndex].RawName != groupName {
			continue
		}
		if observedAt.After(model.Groups[groupIndex].ObservedAt) {
			model.Groups[groupIndex].ObservedAt = observedAt
			model.Groups[groupIndex].ServiceState = sub2ServiceState(status)
			model.Groups[groupIndex].Metrics = sub2Metrics(latencyMS)
		}
		return
	}
	model.Groups = append(model.Groups, domain.GroupObservation{RawName: groupName, ServiceState: sub2ServiceState(status), ObservedAt: observedAt, Metrics: sub2Metrics(latencyMS)})
}

func latestSub2Timeline(points []sub2TimelinePoint) time.Time {
	var latest time.Time
	for _, point := range points {
		parsed, err := time.Parse(time.RFC3339, point.CheckedAt)
		if err == nil && parsed.After(latest) {
			latest = parsed.UTC()
		}
	}
	return latest
}

func sub2ServiceState(status string) domain.ServiceState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "operational", "healthy", "up":
		return domain.ServiceHealthy
	case "degraded", "warning":
		return domain.ServiceDegraded
	case "failed", "error", "down", "unavailable":
		return domain.ServiceFailed
	default:
		return domain.ServiceNoSamples
	}
}

func sub2Metrics(latencyMS *int) domain.Metrics {
	if latencyMS == nil {
		return domain.Metrics{}
	}
	value := float64(*latencyMS)
	return domain.Metrics{AverageLatencyMS: &value}
}
