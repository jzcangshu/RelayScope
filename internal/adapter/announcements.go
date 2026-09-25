package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// sourceOriginBaseURL returns the scheme://host of a site's source URL, so an
// adapter whose base URL is a fronting status page can still reach the NewAPI
// deployment the source actually points at. Empty when the source URL is
// missing or unparseable.
func sourceOriginBaseURL(sourceURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	return parsed.Scheme + "://" + parsed.Host
}

// --- ProbeAdapter (NewAPI系) announcement implementation ---

type newapiAnnouncement struct {
	ID          int    `json:"id"`
	Content     string `json:"content"`
	Extra       string `json:"extra"`
	PublishDate string `json:"publishDate"`
	Type        string `json:"type"`
}

type newapiStatusResponse struct {
	Data struct {
		AnnouncementsEnabled bool                 `json:"announcements_enabled"`
		Announcements        []newapiAnnouncement `json:"announcements"`
		Notice               string               `json:"notice"`
	} `json:"data"`
}

// probeAnnouncementConfig extends probeConfig with announcement fields.
type probeAnnouncementConfig struct {
	StatusBaseURL     string `json:"statusBaseUrl"`
	StatusPath        string `json:"statusPath"`
	AnnouncementMode  string `json:"announcementMode"`
	PricingStatusPath string `json:"pricingStatusPath"`
}

// CollectAnnouncements implements AnnouncementProvider for ProbeAdapter.
// It supports two modes:
//   - "timeline" (default): fetches /api/status and parses announcements[]
//   - "notice_diff": fetches /api/notice and returns as a single announcement
//   - "disabled": returns nil
func (adapter ProbeAdapter) CollectAnnouncements(ctx context.Context, site Site, fetcher Fetcher) ([]Announcement, error) {
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return nil, fmt.Errorf("apply %s config defaults: %w", adapter.Key(), err)
	}
	var config probeAnnouncementConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return nil, fmt.Errorf("decode %s announcement config: %w", adapter.Key(), err)
	}

	mode := strings.TrimSpace(config.AnnouncementMode)
	if mode == "" {
		mode = "timeline"
	}
	if mode == "disabled" {
		return nil, nil
	}

	statusBaseURL := strings.TrimSpace(config.StatusBaseURL)
	if statusBaseURL == "" {
		statusBaseURL = site.BaseURL
	}

	switch mode {
	case "notice_diff":
		return collectNewAPINoticeDiff(ctx, fetcher, statusBaseURL)
	default: // "timeline"
		statusPath := strings.TrimSpace(config.StatusPath)
		if statusPath == "" {
			statusPath = adapter.defaultStatusPath
		}
		if statusPath == "" {
			statusPath = config.PricingStatusPath
		}
		if statusPath == "" {
			statusPath = "/api/status"
		}
		return collectNewAPITimeline(ctx, fetcher, statusBaseURL, statusPath)
	}
}

// collectNewAPITimeline reads a NewAPI /api/status payload and returns every
// published announcement. It is shared by the probe and pricing adapters.
func collectNewAPITimeline(ctx context.Context, fetcher Fetcher, statusBaseURL, statusPath string) ([]Announcement, error) {
	endpoint, err := resolveSiteURL(statusBaseURL, statusPath)
	if err != nil {
		return nil, err
	}
	body, _, err := fetcher.GetBytes(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var resp newapiStatusResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode NewAPI status for announcements: %w", err)
	}
	return parseNewAPITimeline(resp), nil
}

// parseNewAPITimeline turns a decoded /api/status payload into announcements,
// returning nil when the site has the feature off or nothing published.
func parseNewAPITimeline(resp newapiStatusResponse) []Announcement {
	if !resp.Data.AnnouncementsEnabled || len(resp.Data.Announcements) == 0 {
		return nil
	}
	anns := make([]Announcement, 0, len(resp.Data.Announcements))
	for _, item := range resp.Data.Announcements {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		pubTime := parseNewAPITime(item.PublishDate)
		externalID := item.PublishDate
		if externalID == "" {
			externalID = strconv.Itoa(item.ID)
		}
		anns = append(anns, Announcement{
			ExternalID:  externalID,
			Content:     content,
			Type:        strings.TrimSpace(item.Type),
			Extra:       strings.TrimSpace(item.Extra),
			PublishedAt: pubTime,
		})
	}
	return anns
}

// collectNewAPIAnnouncementsAuto is the best-effort variant for adapters that
// sit on top of NewAPI deployments but read models through a different surface
// (status page, activity feed, probe report). The site is probed for a NewAPI
// /api/status timeline; anything that is not a NewAPI status payload — an HTML
// status page, a 404, another API's error envelope — is skipped silently
// instead of surfacing as a collection failure.
//
// Some sites register the monitor under a different host than the NewAPI
// deployment it watches (e.g. an Uptime Kuma status page at status.example.com
// fronting api.example.com). When primaryBaseURL does not answer with a NewAPI
// payload, fallbackBaseURL — usually the monitored source — is tried before
// giving up. Both must already be resolved base URLs; an empty fallback skips
// the second probe.
func collectNewAPIAnnouncementsAuto(ctx context.Context, fetcher Fetcher, primaryBaseURL, fallbackBaseURL, statusPath string) ([]Announcement, error) {
	for _, baseURL := range []string{primaryBaseURL, fallbackBaseURL} {
		if strings.TrimSpace(baseURL) == "" {
			continue
		}
		endpoint, err := resolveSiteURL(baseURL, statusPath)
		if err != nil {
			continue
		}
		body, _, err := fetcher.GetBytes(ctx, endpoint)
		if err != nil {
			continue
		}
		var resp newapiStatusResponse
		if json.Unmarshal(body, &resp) != nil {
			// Not a NewAPI payload (HTML status page, error envelope, ...).
			// Try the fallback host before giving up.
			continue
		}
		// A valid NewAPI payload is authoritative: if announcements are
		// disabled here they are disabled for this site, and probing the
		// fallback host could pick up an unrelated deployment's timeline.
		return parseNewAPITimeline(resp), nil
	}
	return nil, nil
}

// collectNewAPIAnnouncementsFor dispatches announcement collection for adapters
// layered on NewAPI sites. Modes:
//   - "auto" (default): best-effort /api/status, silent when the site is not
//     NewAPI-shaped
//   - "timeline": require a NewAPI status payload, error otherwise
//   - "notice_diff": diff the single /api/notice blob
//   - "disabled": collect nothing
func collectNewAPIAnnouncementsFor(ctx context.Context, fetcher Fetcher, primaryBaseURL, fallbackBaseURL, statusPath, mode string) ([]Announcement, error) {
	switch strings.TrimSpace(mode) {
	case "", "auto":
		return collectNewAPIAnnouncementsAuto(ctx, fetcher, primaryBaseURL, fallbackBaseURL, statusPath)
	case "disabled":
		return nil, nil
	case "notice_diff":
		return collectNewAPINoticeDiff(ctx, fetcher, primaryBaseURL)
	default: // "timeline"
		return collectNewAPITimeline(ctx, fetcher, primaryBaseURL, statusPath)
	}
}

// collectNewAPINoticeDiff reads the single NewAPI /api/notice payload and
// returns it as one announcement; the store detects content changes.
func collectNewAPINoticeDiff(ctx context.Context, fetcher Fetcher, statusBaseURL string) ([]Announcement, error) {
	endpoint, err := resolveSiteURL(statusBaseURL, "/api/notice")
	if err != nil {
		return nil, err
	}
	body, _, err := fetcher.GetBytes(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode notice: %w", err)
	}
	notice := strings.TrimSpace(resp.Data)
	if notice == "" {
		return nil, nil
	}
	return []Announcement{{
		ExternalID:  "__notice__",
		Content:     notice,
		Type:        "default",
		PublishedAt: time.Now().UTC(),
	}}, nil
}

func parseNewAPITime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	// Try RFC3339 first (standard NewAPI format)
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC()
	}
	// Try with milliseconds
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", value); err == nil {
		return t.UTC()
	}
	// Try without timezone
	if t, err := time.Parse("2006-01-02T15:04:05", value); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// CollectAnnouncements implements AnnouncementProvider for NewAPIAdapter.
// NewAPI pricing sites publish announcements through the same /api/status
// timeline the probe adapter reads, so the site the user subscribes to (for
// example 小鸡毛的公益API站) now feeds the notification outbox as well.
//
// Modes mirror the probe adapter: "timeline" (default), "notice_diff", and
// "disabled".
func (adapter NewAPIAdapter) CollectAnnouncements(ctx context.Context, site Site, fetcher Fetcher) ([]Announcement, error) {
	defaulted, err := ApplyConfigDefaults(adapter.ConfigSchema(), json.RawMessage(site.ConfigJSON))
	if err != nil {
		return nil, fmt.Errorf("apply %s config defaults: %w", adapter.Key(), err)
	}
	var config NewAPIConfig
	if err := json.Unmarshal(defaulted, &config); err != nil {
		return nil, fmt.Errorf("decode %s announcement config: %w", adapter.Key(), err)
	}

	mode := strings.TrimSpace(config.AnnouncementMode)
	if mode == "" {
		mode = "timeline"
	}
	if mode == "disabled" {
		return nil, nil
	}

	statusPath := strings.TrimSpace(config.PricingStatusPath)
	if statusPath == "" {
		statusPath = "/api/status"
	}
	if mode == "notice_diff" {
		return collectNewAPINoticeDiff(ctx, fetcher, site.BaseURL)
	}
	return collectNewAPITimeline(ctx, fetcher, site.BaseURL, statusPath)
}

// --- Sub2MonitorAdapter announcement implementation ---

type sub2AnnouncementItem struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type sub2AnnouncementsResponse struct {
	Code    int                      `json:"code"`
	Message string                   `json:"message"`
	Data    []sub2AnnouncementItem   `json:"data"`
}

// CollectAnnouncements implements AnnouncementProvider for Sub2MonitorAdapter.
// It fetches GET /api/v1/announcements with the session's Bearer token.
func (Sub2MonitorAdapter) CollectAnnouncements(ctx context.Context, site Site, fetcher Fetcher) ([]Announcement, error) {
	endpoint, err := resolveSiteURL(site.BaseURL, "/api/v1/announcements")
	if err != nil {
		return nil, err
	}
	body, _, err := fetcher.GetBytes(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var resp sub2AnnouncementsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode sub2api announcements: %w", err)
	}
	if resp.Code != 0 {
		message := strings.TrimSpace(resp.Message)
		if message == "" {
			message = fmt.Sprintf("sub2api announcements returned code %d", resp.Code)
		}
		return nil, fmt.Errorf("%s", message)
	}
	anns := make([]Announcement, 0, len(resp.Data))
	for _, item := range resp.Data {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		pubTime := parseSub2Time(item.CreatedAt)
		anns = append(anns, Announcement{
			ExternalID:  strconv.Itoa(item.ID),
			Title:       strings.TrimSpace(item.Title),
			Content:     content,
			Type:        "default",
			PublishedAt: pubTime,
		})
	}
	return anns, nil
}

func parseSub2Time(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	// Try RFC3339 with timezone
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC()
	}
	// Try with microseconds and timezone offset
	if t, err := time.Parse("2006-01-02T15:04:05.000000-07:00", value); err == nil {
		return t.UTC()
	}
	// Try without timezone
	if t, err := time.Parse("2006-01-02T15:04:05", value); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
