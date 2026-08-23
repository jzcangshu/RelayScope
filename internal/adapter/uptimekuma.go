package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"relaypulse/internal/domain"
	"relaypulse/internal/pricing"
)

const (
	uptimeKumaHistoryWindow  = 72 * time.Hour
	uptimeKumaMetricWindow   = 24 * time.Hour
	uptimeKumaDefaultRefresh = 5 * time.Minute
	uptimeKumaMaxCadence     = 7 * 24 * time.Hour
)

// UptimeKumaAdapter reads the public status-page API. Heartbeats are mapped by
// their real timestamps so monitors with different check intervals share one
// dashboard timeline without stretching stale or sparse samples to the present.
type UptimeKumaAdapter struct {
	PricingRegistry *pricing.Registry
}

func (UptimeKumaAdapter) Key() string         { return "uptime-kuma" }
func (UptimeKumaAdapter) DisplayName() string { return "Uptime Kuma 状态页" }
func (UptimeKumaAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"slug":{"type":"string"},"statusBaseUrl":{"type":"string"},"statusPath":{"type":"string","default":"/api/status-page/{slug}"},"heartbeatPath":{"type":"string","default":"/api/status-page/heartbeat/{slug}"},"retryAttempts":{"type":"integer","minimum":0,"maximum":3},"monitorNameMode":{"type":"string","enum":["suffix-model"]},"pricingAdapter":{"type":"string"},"pricingBaseUrl":{"type":"string"},"pricingPath":{"type":"string"},"pricingStatusPath":{"type":"string"},"pricingOptional":{"type":"boolean"},"pricingRequiresSession":{"type":"boolean"}}}`)
}

type uptimeKumaConfig struct {
	Slug              string `json:"slug"`
	StatusBaseURL     string `json:"statusBaseUrl"`
	StatusPath        string `json:"statusPath"`
	HeartbeatPath     string `json:"heartbeatPath"`
	RetryAttempts     int    `json:"retryAttempts"`
	MonitorNameMode   string `json:"monitorNameMode"`
	PricingAdapter    string `json:"pricingAdapter"`
	PricingBaseURL    string `json:"pricingBaseUrl"`
	PricingPath       string `json:"pricingPath"`
	PricingStatusPath string `json:"pricingStatusPath"`
	PricingOptional   bool   `json:"pricingOptional"`
	PricingNeedsLogin bool   `json:"pricingRequiresSession"`
}

type uptimeKumaStatusPage struct {
	Config struct {
		AutoRefreshInterval float64 `json:"autoRefreshInterval"`
	} `json:"config"`
	PublicGroupList []struct {
		Name        string `json:"name"`
		MonitorList []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"monitorList"`
	} `json:"publicGroupList"`
}

type uptimeKumaHeartbeatPayload struct {
	HeartbeatList map[string][]uptimeKumaHeartbeat `json:"heartbeatList"`
}

type uptimeKumaHeartbeat struct {
	Status   int     `json:"status"`
	Time     string  `json:"time"`
	Duration float64 `json:"duration"`
}

type uptimeKumaSample struct {
	State domain.ServiceState
	Time  time.Time
}

type uptimeKumaModel struct {
	observation    domain.ModelObservation
	groups         map[string]struct{}
	historyStart   time.Time
	historyCovered bool
}

func (adapter UptimeKumaAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	config, err := decodeUptimeKumaConfig(site)
	if err != nil {
		return domain.Collection{}, err
	}
	nameMode := strings.ToLower(strings.TrimSpace(config.MonitorNameMode))
	if nameMode != "" && nameMode != "suffix-model" {
		return domain.Collection{}, fmt.Errorf("unsupported Uptime Kuma monitor name mode %q", config.MonitorNameMode)
	}
	slug, err := uptimeKumaSlug(site, config)
	if err != nil {
		return domain.Collection{}, err
	}
	statusBaseURL := strings.TrimSpace(config.StatusBaseURL)
	if statusBaseURL == "" {
		statusBaseURL = site.BaseURL
	}
	statusPath := config.StatusPath
	if strings.TrimSpace(statusPath) == "" {
		statusPath = "/api/status-page/{slug}"
	}
	statusPath = strings.ReplaceAll(statusPath, "{slug}", url.PathEscape(slug))
	statusURL, err := resolveSiteURL(statusBaseURL, statusPath)
	if err != nil {
		return domain.Collection{}, err
	}
	heartbeatPath := config.HeartbeatPath
	if strings.TrimSpace(heartbeatPath) == "" {
		heartbeatPath = "/api/status-page/heartbeat/{slug}"
	}
	heartbeatPath = strings.ReplaceAll(heartbeatPath, "{slug}", url.PathEscape(slug))
	heartbeatURL, err := resolveSiteURL(statusBaseURL, heartbeatPath)
	if err != nil {
		return domain.Collection{}, err
	}
	retries := config.RetryAttempts
	if retries < 0 {
		retries = 0
	}
	if retries > 3 {
		retries = 3
	}
	var page uptimeKumaStatusPage
	var heartbeatPayload uptimeKumaHeartbeatPayload
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		page = uptimeKumaStatusPage{}
		heartbeatPayload = uptimeKumaHeartbeatPayload{}
		if fetchErr := fetcher.GetJSON(ctx, statusURL, &page); fetchErr != nil {
			lastErr = fmt.Errorf("fetch Uptime Kuma status page: %w", fetchErr)
			continue
		}
		if len(page.PublicGroupList) == 0 {
			lastErr = fmt.Errorf("Uptime Kuma status page %q contains no public monitors", slug)
			continue
		}
		if fetchErr := fetcher.GetJSON(ctx, heartbeatURL, &heartbeatPayload); fetchErr != nil {
			lastErr = fmt.Errorf("fetch Uptime Kuma heartbeats: %w", fetchErr)
			continue
		}
		if !hasUptimeKumaHeartbeats(heartbeatPayload) {
			lastErr = fmt.Errorf("Uptime Kuma status page %q returned no heartbeat data", slug)
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return domain.Collection{}, lastErr
	}

	refresh := durationFromSeconds(page.Config.AutoRefreshInterval)
	if refresh <= 0 {
		refresh = uptimeKumaDefaultRefresh
	}
	builders := make(map[string]*uptimeKumaModel)
	for _, group := range page.PublicGroupList {
		groupName := strings.TrimSpace(group.Name)
		if groupName == "" {
			groupName = "default"
		}
		for _, monitor := range group.MonitorList {
			modelName, uniqueGroupName := uptimeKumaMonitorIdentity(groupName, monitor.Name, nameMode)
			if modelName == "" || monitor.ID <= 0 {
				continue
			}
			builder := builders[modelName]
			if builder == nil {
				builder = &uptimeKumaModel{
					observation:    domain.ModelObservation{RawName: modelName, Provider: groupName},
					groups:         make(map[string]struct{}),
					historyCovered: true,
				}
				builders[modelName] = builder
			}
			if _, exists := builder.groups[uniqueGroupName]; exists {
				uniqueGroupName += " #" + strconv.FormatInt(monitor.ID, 10)
			}
			builder.groups[uniqueGroupName] = struct{}{}
			monitorID := strconv.FormatInt(monitor.ID, 10)
			groupObservation, historyStart, historyCovered := buildUptimeKumaGroup(uniqueGroupName, heartbeatPayload.HeartbeatList[monitorID], refresh, now.UTC())
			builder.observation.Groups = append(builder.observation.Groups, groupObservation)
			if !historyCovered {
				builder.historyCovered = false
			} else if historyStart.After(builder.historyStart) {
				builder.historyStart = historyStart
			}
		}
	}
	if len(builders) == 0 {
		return domain.Collection{}, fmt.Errorf("Uptime Kuma status page %q contains no valid monitor names", slug)
	}

	modelNames := make([]string, 0, len(builders))
	for name := range builders {
		modelNames = append(modelNames, name)
	}
	sort.Strings(modelNames)
	collection := domain.Collection{
		SiteID: site.ID, ObservedAt: now.UTC(), CollectedAt: now.UTC(), CatalogComplete: true,
		CatalogRawNames: modelNames,
	}
	for _, name := range modelNames {
		builder := builders[name]
		if builder.historyCovered && !builder.historyStart.IsZero() {
			builder.observation.HistoryCoverageStart = builder.historyStart
			builder.observation.HistoryCoverageEnd = now.UTC()
		}
		collection.Models = append(collection.Models, builder.observation)
	}
	if config.PricingAdapter != "" && config.PricingPath != "" {
		if err := attachPricingSource(ctx, site, fetcher, adapter.PricingRegistry, pricingSource{
			DecoderKey: config.PricingAdapter, BaseURL: config.PricingBaseURL,
			Path: config.PricingPath, StatusPath: config.PricingStatusPath, Optional: config.PricingOptional,
		}, &collection); err != nil {
			return domain.Collection{}, fmt.Errorf("attach Uptime Kuma pricing: %w", err)
		}
	}
	return collection, nil
}

func uptimeKumaMonitorIdentity(pageGroup, monitorName, mode string) (string, string) {
	modelName := strings.TrimSpace(monitorName)
	groupName := pageGroup
	if mode != "suffix-model" {
		return modelName, groupName
	}
	parts := strings.Fields(modelName)
	if len(parts) < 2 {
		return modelName, groupName
	}
	modelName = parts[len(parts)-1]
	groupName = strings.Join(parts[:len(parts)-1], " ")
	if groupName == "" {
		groupName = pageGroup
	}
	return modelName, groupName
}

func hasUptimeKumaHeartbeats(payload uptimeKumaHeartbeatPayload) bool {
	for _, heartbeats := range payload.HeartbeatList {
		if len(heartbeats) > 0 {
			return true
		}
	}
	return false
}

func decodeUptimeKumaConfig(site Site) (uptimeKumaConfig, error) {
	defaulted, err := ApplyConfigDefaults(UptimeKumaAdapter{}.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return uptimeKumaConfig{}, fmt.Errorf("apply uptime-kuma config defaults: %w", err)
	}
	var config uptimeKumaConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return uptimeKumaConfig{}, fmt.Errorf("decode uptime-kuma config: %w", err)
	}
	return config, nil
}

func uptimeKumaSlug(site Site, config uptimeKumaConfig) (string, error) {
	if slug := strings.TrimSpace(config.Slug); slug != "" {
		return slug, nil
	}
	parsed, err := url.Parse(site.SourceURL)
	if err != nil {
		return "", fmt.Errorf("parse Uptime Kuma source URL: %w", err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) >= 2 && parts[len(parts)-2] == "status" && parts[len(parts)-1] != "" {
		return parts[len(parts)-1], nil
	}
	return "", fmt.Errorf("uptime-kuma source URL must end with /status/{slug}")
}

func buildUptimeKumaGroup(name string, heartbeats []uptimeKumaHeartbeat, refresh time.Duration, now time.Time) (domain.GroupObservation, time.Time, bool) {
	samples := make([]uptimeKumaSample, 0, len(heartbeats))
	reportedDurations := make([]time.Duration, 0, len(heartbeats))
	for _, heartbeat := range heartbeats {
		observedAt, ok := parseUptimeKumaTime(heartbeat.Time)
		if !ok || observedAt.After(now.Add(refresh)) {
			continue
		}
		samples = append(samples, uptimeKumaSample{State: uptimeKumaState(heartbeat.Status), Time: observedAt.UTC()})
		if duration := durationFromSeconds(heartbeat.Duration); duration > 0 && duration <= uptimeKumaMaxCadence {
			reportedDurations = append(reportedDurations, duration)
		}
	}
	sort.SliceStable(samples, func(left, right int) bool { return samples[left].Time.Before(samples[right].Time) })
	group := domain.GroupObservation{RawName: name, ServiceState: domain.ServiceNoSamples}
	if len(samples) == 0 {
		return group, time.Time{}, false
	}
	gaps := make([]time.Duration, 0, len(samples)-1)
	for index := 1; index < len(samples); index++ {
		gap := samples[index].Time.Sub(samples[index-1].Time)
		if gap > 0 && gap <= uptimeKumaMaxCadence {
			gaps = append(gaps, gap)
		}
	}
	cadence := medianDuration(gaps)
	if cadence <= 0 {
		cadence = medianDuration(reportedDurations)
	}
	if cadence <= 0 {
		cadence = refresh
	}
	grace := maxDuration(3*cadence, 2*refresh)
	if grace > uptimeKumaMaxCadence {
		grace = uptimeKumaMaxCadence
	}
	latest := samples[len(samples)-1]
	group.ObservedAt = latest.Time
	if !latest.Time.Before(now.Add(-grace)) {
		group.ServiceState = latest.State
	}

	retentionStart := now.Add(-uptimeKumaHistoryWindow)
	historyStart := samples[0].Time
	if historyStart.Before(retentionStart) {
		historyStart = retentionStart
	}
	for index, sample := range samples {
		end := sample.Time.Add(grace)
		if index+1 < len(samples) && samples[index+1].Time.Before(end) {
			end = samples[index+1].Time
		}
		if end.After(now) {
			end = now
		}
		start := sample.Time
		if start.Before(retentionStart) {
			start = retentionStart
		}
		if !end.After(start) {
			continue
		}
		resolution := end.Sub(start).Round(time.Second)
		if resolution < time.Second {
			resolution = time.Second
		}
		group.Buckets = append(group.Buckets, domain.TimeBucket{
			Start: start, End: end, Resolution: resolution,
			Metrics: domain.Metrics{SuccessRatio: uptimeKumaStateRatio(sample.State)},
		})
	}
	group.Metrics = uptimeKumaWindowMetrics(group.Buckets, now.Add(-uptimeKumaMetricWindow), now)
	return group, historyStart, true
}

func parseUptimeKumaTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05Z07:00"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func uptimeKumaState(status int) domain.ServiceState {
	switch status {
	case 1:
		return domain.ServiceHealthy
	case 0:
		return domain.ServiceFailed
	case 2:
		return domain.ServiceDegraded
	default:
		return domain.ServiceNoSamples
	}
}

func uptimeKumaStateRatio(state domain.ServiceState) *float64 {
	var ratio float64
	switch state {
	case domain.ServiceHealthy:
		ratio = 1
	case domain.ServiceDegraded:
		ratio = 0.5
	case domain.ServiceFailed:
		ratio = 0
	default:
		return nil
	}
	return &ratio
}

func uptimeKumaWindowMetrics(buckets []domain.TimeBucket, start, end time.Time) domain.Metrics {
	var known, weighted time.Duration
	for _, bucket := range buckets {
		if bucket.Metrics.SuccessRatio == nil {
			continue
		}
		overlapStart := bucket.Start
		if overlapStart.Before(start) {
			overlapStart = start
		}
		overlapEnd := bucket.End
		if overlapEnd.After(end) {
			overlapEnd = end
		}
		if !overlapEnd.After(overlapStart) {
			continue
		}
		duration := overlapEnd.Sub(overlapStart)
		known += duration
		weighted += time.Duration(float64(duration) * *bucket.Metrics.SuccessRatio)
	}
	if known <= 0 {
		return domain.Metrics{}
	}
	ratio := float64(weighted) / float64(known)
	return domain.Metrics{SuccessRatio: &ratio}
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func medianDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return ordered[middle-1] + (ordered[middle]-ordered[middle-1])/2
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}
