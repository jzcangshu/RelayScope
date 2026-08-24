package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"relayscope/internal/adapter/adapterutil"
	"relayscope/internal/domain"
)

// AIAPIAdapter reads the public AIAPI availability matrix and keeps the
// source's channel grouping and timestamped timeline intact.
type AIAPIAdapter struct{}

func (AIAPIAdapter) Key() string         { return "aiapi-probe" }
func (AIAPIAdapter) DisplayName() string { return "AIAPI 状态矩阵" }
func (AIAPIAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"statusPath":{"type":"string","default":"/api/status"},"period":{"type":"string","default":"24h"},"board":{"type":"string","default":"hot"}}}`)
}

type aiAPIConfig struct {
	StatusPath string `json:"statusPath"`
	Period     string `json:"period"`
	Board      string `json:"board"`
}

type aiAPIResponse struct {
	Groups []aiAPIGroup `json:"groups"`
}

type aiAPIGroup struct {
	Provider string       `json:"provider"`
	Channel  string       `json:"channel"`
	Layers   []aiAPILayer `json:"layers"`
}

type aiAPILayer struct {
	Model         string          `json:"model"`
	CurrentStatus aiAPICurrent    `json:"current_status"`
	Timeline      []aiAPITimeline `json:"timeline"`
}

type aiAPICurrent struct {
	Status    int     `json:"status"`
	Latency   float64 `json:"latency"`
	Timestamp int64   `json:"timestamp"`
}

type aiAPITimeline struct {
	Timestamp    int64   `json:"timestamp"`
	Status       int     `json:"status"`
	Latency      float64 `json:"latency"`
	Availability float64 `json:"availability"`
}

func (adapter AIAPIAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return domain.Collection{}, fmt.Errorf("apply AIAPI config defaults: %w", err)
	}
	var config aiAPIConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return domain.Collection{}, fmt.Errorf("decode AIAPI config: %w", err)
	}
	endpoint, err := resolveSiteURL(site.BaseURL, config.StatusPath)
	if err != nil {
		return domain.Collection{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return domain.Collection{}, err
	}
	query := parsed.Query()
	query.Set("period", config.Period)
	if strings.TrimSpace(config.Board) != "" {
		query.Set("board", config.Board)
	}
	parsed.RawQuery = query.Encode()
	endpoint = parsed.String()

	var payload aiAPIResponse
	if err := fetcher.GetJSON(ctx, endpoint, &payload); err != nil {
		return domain.Collection{}, err
	}
	if len(payload.Groups) == 0 {
		return domain.Collection{}, fmt.Errorf("AIAPI response contained no groups")
	}

	modelIndexes := make(map[string]int)
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now.UTC(), CollectedAt: now.UTC(), CatalogComplete: true}
	for _, sourceGroup := range payload.Groups {
		groupName := strings.TrimSpace(sourceGroup.Channel)
		if groupName == "" {
			groupName = "default"
		}
		for _, layer := range sourceGroup.Layers {
			modelName := strings.TrimSpace(layer.Model)
			if modelName == "" {
				modelName = strings.TrimSpace(sourceGroup.Provider)
			}
			if modelName == "" {
				continue
			}
			modelIndex, exists := modelIndexes[modelName]
			if !exists {
				modelIndex = len(collection.Models)
				modelIndexes[modelName] = modelIndex
				collection.Models = append(collection.Models, domain.ModelObservation{RawName: modelName, Provider: strings.TrimSpace(sourceGroup.Provider)})
				collection.CatalogRawNames = append(collection.CatalogRawNames, modelName)
			}
			model := &collection.Models[modelIndex]
			group := domain.GroupObservation{RawName: groupName, ServiceState: aiAPIState(layer.CurrentStatus.Status), ObservedAt: aiAPITimestamp(layer.CurrentStatus.Timestamp)}
			if layer.CurrentStatus.Latency > 0 {
				latency := layer.CurrentStatus.Latency
				group.Metrics.AverageLatencyMS = &latency
			}
			points := append([]aiAPITimeline(nil), layer.Timeline...)
			sort.SliceStable(points, func(left, right int) bool { return points[left].Timestamp < points[right].Timestamp })
			for index, point := range points {
				start := aiAPITimestamp(point.Timestamp)
				if start.IsZero() || start.After(now.UTC()) {
					continue
				}
				end := now.UTC()
				for next := index + 1; next < len(points); next++ {
					nextStart := aiAPITimestamp(points[next].Timestamp)
					if nextStart.After(start) {
						end = nextStart
						break
					}
				}
				if end.After(now.UTC()) {
					end = now.UTC()
				}
				if !end.After(start) {
					continue
				}
				metrics := domain.Metrics{SuccessRatio: aiAPIRatio(point.Availability)}
				if point.Latency > 0 {
					latency := point.Latency
					metrics.AverageLatencyMS = &latency
				}
				group.Buckets = append(group.Buckets, domain.TimeBucket{Start: start, End: end, Resolution: end.Sub(start), Metrics: metrics})
				if group.ObservedAt.IsZero() || start.After(group.ObservedAt) {
					group.ObservedAt = start
					group.ServiceState = aiAPIState(point.Status)
					group.Metrics = metrics
				}
			}
			if len(group.Buckets) > 0 {
				model.HistoryCoverageStart = group.Buckets[0].Start
				model.HistoryCoverageEnd = now.UTC()
			}
			if existing := findAIAPIGroup(model.Groups, groupName); existing >= 0 {
				model.Groups[existing] = group
			} else {
				model.Groups = append(model.Groups, group)
			}
		}
	}
	if len(collection.Models) == 0 {
		return domain.Collection{}, fmt.Errorf("AIAPI response contained no valid models")
	}
	return collection, nil
}

func findAIAPIGroup(groups []domain.GroupObservation, name string) int {
	for index := range groups {
		if groups[index].RawName == name {
			return index
		}
	}
	return -1
}

func aiAPITimestamp(value int64) time.Time {
	return adapterutil.ParseFlexibleTime(value)
}

func aiAPIState(status int) domain.ServiceState {
	switch status {
	case 1:
		return domain.ServiceHealthy
	case 2:
		return domain.ServiceDegraded
	case 0:
		return domain.ServiceFailed
	default:
		return domain.ServiceNoSamples
	}
}

func aiAPIRatio(value float64) *float64 {
	if value < 0 {
		return nil
	}
	if value > 1 {
		value /= 100
	}
	return &value
}
