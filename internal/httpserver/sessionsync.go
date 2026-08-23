package httpserver

import (
	"context"
	"net/http"
	"relaypulse/internal/domain"
	"relaypulse/internal/linuxdo"
	"relaypulse/internal/session"
	"relaypulse/internal/store"
	"strings"
	"time"
)

func linuxDOUser(service *linuxdo.Service, request *http.Request) (store.User, bool) {
	if service == nil {
		return store.User{}, false
	}
	cookie, err := request.Cookie("relaypulse_user")
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return store.User{}, false
	}
	return service.UserBySession(cookie.Value)
}

func authorizeSessionSync(request *http.Request, manager *session.SyncManager) (string, string, bool) {
	origin := request.Header.Get("Origin")
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		return origin, "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	return origin, token, manager != nil && manager.Authorize(token, origin)
}

func setExtensionCORS(writer http.ResponseWriter, origin string) {
	writer.Header().Set("Access-Control-Allow-Origin", origin)
	writer.Header().Set("Vary", "Origin")
}

func pendingSessionSites(ctx context.Context, dbStore *store.Store, now time.Time) ([]sessionSyncSite, error) {
	sites, err := dbStore.ListEnabledSites(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]sessionSyncSite, 0)
	for _, site := range sites {
		if !site.SessionRequired {
			continue
		}
		expiresAt, exists, err := dbStore.SessionExpiresAt(ctx, site.ID, session.SessionPurpose)
		if err != nil {
			return nil, err
		}
		reason := ""
		switch {
		case site.AcquisitionState == domain.AcquisitionLoginExpired:
			reason = "login_expired"
		case site.AcquisitionState == domain.AcquisitionCollectionFailed && exists:
			reason = "verification_failed"
		case exists && expiresAt != nil && !expiresAt.After(now.UTC().Add(24*time.Hour)):
			reason = "expiring"
		case !exists:
			reason = "login_required"
		}
		if reason == "" {
			continue
		}
		origin, err := session.NormalizeOrigin(site.BaseURL)
		if err != nil {
			continue
		}
		loginURL := strings.TrimSpace(site.BaseURL)
		if _, err := session.NormalizeOrigin(loginURL); err != nil {
			loginURL = origin
		}
		items = append(items, sessionSyncSite{ID: site.ID, Name: site.Name, Origin: origin, LoginURL: loginURL, SourceURL: site.SourceURL, Reason: reason, AdapterKey: site.AdapterKey})
	}
	return items, nil
}

func timePtr(value time.Time) *time.Time { return &value }
