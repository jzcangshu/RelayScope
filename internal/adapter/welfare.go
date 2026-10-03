package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"relayscope/internal/adapter/adapterutil"
	"relayscope/internal/domain"
)

// WelfareAdapter collects the public availability report used by Darkforger-style
// welfare fronts. The page draws either 7 daily bars or 24 hourly bars from the
// same payload; the daily view is capped by the newest 512 checks and hides older
// days, so collection follows the 24-hour hourly view. No session is required.
type WelfareAdapter struct{}

func (WelfareAdapter) Key() string         { return "welfare-availability" }
func (WelfareAdapter) DisplayName() string { return "公益站服务状态" }
func (WelfareAdapter) ConfigSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"availabilityPath":{"type":"string","default":"/api/welfare/availability"},"noticesPath":{"type":"string","default":"/api/welfare/notices?size=50"},"announcementMode":{"type":"string","enum":["auto","disabled"],"default":"auto"}}}`)
}

type welfareConfig struct {
	AvailabilityPath string `json:"availabilityPath"`
	NoticesPath      string `json:"noticesPath"`
	AnnouncementMode string `json:"announcementMode"`
}

type welfareAvailabilityResponse struct {
	Success *bool               `json:"success"`
	Message string              `json:"message"`
	Data    welfareAvailability `json:"data"`
}

type welfareAvailability struct {
	Groups []welfareGroup `json:"groups"`
}

type welfareGroup struct {
	ID     string         `json:"id"`
	Models []welfareModel `json:"models"`
}

type welfareModel struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	LatencyMS *float64       `json:"latency_ms"`
	CheckedAt string         `json:"checked_at"`
	History   []welfareCheck `json:"history"`
}

type welfareCheck struct {
	CheckedAt string   `json:"checked_at"`
	Status    string   `json:"status"`
	LatencyMS *float64 `json:"latency_ms"`
	Success   *float64 `json:"success"`
	Total     *float64 `json:"total"`
}

type welfareNoticesResponse struct {
	Success *bool  `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Items []welfareNotice `json:"items"`
	} `json:"data"`
}

type welfareNotice struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Level     string `json:"level"`
	CreatedAt string `json:"createdAt"`
}

// welfareTotals accumulates real checks. Latency is weighted by each check's
// sample count, matching the page's hourly rollup.
type welfareTotals struct {
	success       int64
	total         int64
	latencySum    float64
	latencyWeight int64
	latest        time.Time
}

func (WelfareAdapter) Collect(ctx context.Context, site Site, fetcher Fetcher, now time.Time) (domain.Collection, error) {
	config, err := welfareConfigFrom(site.ConfigJSON)
	if err != nil {
		return domain.Collection{}, err
	}
	endpoint, err := resolveSiteURL(site.BaseURL, config.AvailabilityPath)
	if err != nil {
		return domain.Collection{}, err
	}
	var payload welfareAvailabilityResponse
	if err := fetcher.GetJSON(ctx, endpoint, &payload); err != nil {
		return domain.Collection{}, err
	}
	if payload.Success != nil && !*payload.Success {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "welfare availability response was unsuccessful"
		}
		return domain.Collection{}, fmt.Errorf("%s", message)
	}

	now = now.UTC()
	windowStart := welfareWindowStart(now)
	collection := domain.Collection{SiteID: site.ID, ObservedAt: now, CollectedAt: now, CatalogComplete: true}
	type builtModel struct {
		groups  []domain.GroupObservation
		covered bool
	}
	order := make([]string, 0)
	byName := make(map[string]*builtModel)
	for _, group := range payload.Data.Groups {
		groupName := strings.TrimSpace(group.ID)
		if groupName == "" {
			groupName = "default"
		}
		seen := make(map[string]bool, len(group.Models))
		for _, model := range group.Models {
			name := strings.TrimSpace(model.ID)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			observation, covered := welfareGroupObservation(model, groupName, windowStart, now)
			built := byName[name]
			if built == nil {
				built = &builtModel{covered: true}
				byName[name] = built
				order = append(order, name)
			}
			built.groups = append(built.groups, observation)
			if !covered {
				built.covered = false
			}
		}
	}
	if len(order) == 0 {
		return markEmptyProbeCatalog(collection), nil
	}
	for _, name := range order {
		built := byName[name]
		observation := domain.ModelObservation{RawName: name, Groups: built.groups}
		if built.covered {
			observation.HistoryCoverageStart = windowStart
			observation.HistoryCoverageEnd = now
		}
		collection.Models = append(collection.Models, observation)
		collection.CatalogRawNames = append(collection.CatalogRawNames, name)
	}
	return collection, nil
}

// welfareGroupObservation folds one model's checks into the 24 hour grid the
// status page uses: buckets are UTC hours, the grid opens at the next hour
// boundary minus 24 hours, and checks after "now" are ignored. Coverage is
// claimed only when the returned history reaches that opening (so the newest-512
// cap did not drop anything inside the window) and a real check landed within
// the last hour.
func welfareGroupObservation(model welfareModel, groupName string, windowStart, now time.Time) (domain.GroupObservation, bool) {
	history := model.History
	if len(history) == 0 && welfareKnownStatus(model.Status) && strings.TrimSpace(model.CheckedAt) != "" {
		history = []welfareCheck{{CheckedAt: model.CheckedAt, Status: model.Status, LatencyMS: model.LatencyMS}}
	}
	var oldest time.Time
	var current welfareTotals
	buckets := make(map[time.Time]*welfareTotals)
	for _, check := range history {
		when, ok := parseWelfareTime(check.CheckedAt)
		if !ok {
			continue
		}
		if oldest.IsZero() || when.Before(oldest) {
			oldest = when
		}
		if when.Before(windowStart) || when.After(now) {
			continue
		}
		success, total, counted := welfareCheckCounts(check)
		if !counted {
			continue
		}
		start := when.Truncate(time.Hour)
		bucket := buckets[start]
		if bucket == nil {
			bucket = &welfareTotals{}
			buckets[start] = bucket
		}
		bucket.add(when, success, total, check.LatencyMS)
		current.add(when, success, total, check.LatencyMS)
	}
	group := domain.GroupObservation{
		RawName:      groupName,
		ServiceState: adapterutil.RatioToServiceState(current.metrics().SuccessRatio),
		ObservedAt:   current.latest,
		Metrics:      current.metrics(),
	}
	starts := make([]time.Time, 0, len(buckets))
	for start := range buckets {
		starts = append(starts, start)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	for _, start := range starts {
		group.Buckets = append(group.Buckets, domain.TimeBucket{
			Start:      start,
			End:        start.Add(time.Hour),
			Resolution: time.Hour,
			Metrics:    buckets[start].metrics(),
		})
	}
	fresh := !current.latest.IsZero() && !current.latest.Before(now.Add(-time.Hour))
	covered := !oldest.IsZero() && !oldest.After(windowStart) && fresh
	return group, covered
}

func (totals *welfareTotals) add(when time.Time, success, total int64, latency *float64) {
	totals.success += success
	totals.total += total
	if latency != nil && !math.IsNaN(*latency) && !math.IsInf(*latency, 0) && *latency >= 0 {
		totals.latencySum += *latency * float64(total)
		totals.latencyWeight += total
	}
	if totals.latest.IsZero() || when.After(totals.latest) {
		totals.latest = when
	}
}

func (totals welfareTotals) metrics() domain.Metrics {
	metrics := domain.Metrics{}
	if totals.total <= 0 {
		return metrics
	}
	ratio := float64(totals.success) / float64(totals.total)
	metrics.SuccessRatio = &ratio
	metrics.RequestCount = int64Pointer(totals.total)
	metrics.SuccessCount = int64Pointer(totals.success)
	metrics.FailureCount = int64Pointer(totals.total - totals.success)
	if totals.latencyWeight > 0 {
		average := totals.latencySum / float64(totals.latencyWeight)
		metrics.AverageLatencyMS = &average
	}
	return metrics
}

// welfareCheckCounts prefers explicit success/total counters. A check that omits
// them still counts as one sample, successful only when the page would call it
// operational. Unknown checks are not samples.
func welfareCheckCounts(check welfareCheck) (success, total int64, counted bool) {
	if check.Total != nil && check.Success != nil && *check.Total == math.Trunc(*check.Total) && *check.Success == math.Trunc(*check.Success) {
		total = int64(*check.Total)
		success = int64(*check.Success)
		if total > 0 && success >= 0 && success <= total {
			return success, total, true
		}
	}
	switch strings.ToLower(strings.TrimSpace(check.Status)) {
	case "operational":
		return 1, 1, true
	case "degraded", "unavailable":
		return 0, 1, true
	default:
		return 0, 0, false
	}
}

func welfareKnownStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "operational", "degraded", "unavailable":
		return true
	default:
		return false
	}
}

// welfareWindowStart is the opening of the 24 hourly bars. The page aligns the
// grid to the next UTC hour, then steps back 24 hours, and drops checks after now.
func welfareWindowStart(now time.Time) time.Time {
	now = now.UTC()
	return now.Truncate(time.Hour).Add(time.Hour).Add(-24 * time.Hour)
}

func parseWelfareTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func welfareConfigFrom(raw string) (welfareConfig, error) {
	defaulted, err := ApplyConfigDefaults(WelfareAdapter{}.ConfigSchema(), json.RawMessage(raw))
	if err != nil {
		return welfareConfig{}, fmt.Errorf("apply welfare-availability config defaults: %w", err)
	}
	var config welfareConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return welfareConfig{}, fmt.Errorf("decode welfare-availability config: %w", err)
	}
	return config, nil
}

// CollectAnnouncements implements AnnouncementProvider. The banner on the status
// page is /api/welfare/notices, not the NewAPI timeline in /api/status.
func (WelfareAdapter) CollectAnnouncements(ctx context.Context, site Site, fetcher Fetcher) ([]Announcement, error) {
	config, err := welfareConfigFrom(site.ConfigJSON)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.AnnouncementMode) == "disabled" {
		return nil, nil
	}
	endpoint, err := resolveSiteURL(site.BaseURL, config.NoticesPath)
	if err != nil {
		return nil, err
	}
	var payload welfareNoticesResponse
	if err := fetcher.GetJSON(ctx, endpoint, &payload); err != nil {
		return nil, err
	}
	if payload.Success != nil && !*payload.Success {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "welfare notices response was unsuccessful"
		}
		return nil, fmt.Errorf("%s", message)
	}
	announcements := make([]Announcement, 0, len(payload.Data.Items))
	for _, item := range payload.Data.Items {
		if item.ID <= 0 {
			continue
		}
		announcement := Announcement{
			ExternalID: strconv.FormatInt(item.ID, 10),
			Title:      strings.TrimSpace(item.Title),
			Content:    item.Content,
			Type:       strings.TrimSpace(item.Level),
		}
		if published, ok := parseWelfareTime(item.CreatedAt); ok {
			announcement.PublishedAt = published
		}
		announcements = append(announcements, announcement)
	}
	return announcements, nil
}
