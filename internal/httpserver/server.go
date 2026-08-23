package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"relaypulse/internal/admin"
	"relaypulse/internal/collector"
	"relaypulse/internal/domain"
	"relaypulse/internal/linuxdo"
	"relaypulse/internal/matcher"
	"relaypulse/internal/session"
	"relaypulse/internal/store"
	webassets "relaypulse/web"
)

type Options struct {
	Logger       *slog.Logger
	Version      string
	Now          func() time.Time
	Store        *store.Store
	Auth         *admin.Auth
	Collector    *collector.Collector
	SessionVault *session.Vault
	PublicURL    string
	SessionSync  *session.SyncManager
	LinuxDO      *linuxdo.Service
}

type sessionSyncSite struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Origin     string `json:"origin"`
	LoginURL   string `json:"loginUrl"`
	SourceURL  string `json:"sourceUrl"`
	Reason     string `json:"reason"`
	AdapterKey string `json:"adapterKey"`
}

type sessionSyncBundle struct {
	SiteID int64  `json:"siteId"`
	Origin string `json:"origin"`
	session.Data
}

type publicDashboardCache struct {
	mu          sync.Mutex
	revision    int64
	initialized bool
	payload     []byte
}

func (cache *publicDashboardCache) load(ctx context.Context, dbStore *store.Store, now time.Time) ([]byte, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		revision, err := dbStore.Revision(ctx)
		if err != nil {
			return nil, err
		}
		if cache.initialized && cache.revision == revision {
			return cache.payload, nil
		}
		rows, err := dbStore.QueryPublicRows(ctx, "", "")
		if err != nil {
			return nil, err
		}
		history, err := dbStore.QueryPublicHistory(ctx, now.UTC().Add(-24*time.Hour))
		if err != nil {
			return nil, err
		}
		if latestRevision, revisionErr := dbStore.Revision(ctx); revisionErr != nil {
			return nil, revisionErr
		} else if latestRevision != revision {
			continue
		}
		payload, err := json.Marshal(map[string]any{"revision": revision, "rows": rows, "buckets": history, "hours": 24})
		if err != nil {
			return nil, fmt.Errorf("encode public dashboard: %w", err)
		}
		cache.revision = revision
		cache.initialized = true
		cache.payload = payload
		return cache.payload, nil
	}
	return nil, errors.New("public dashboard changed during read")
}

func NewHandler(options Options) (http.Handler, error) {
	if options.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.SessionSync == nil {
		options.SessionSync = session.NewSyncManager(options.Now)
	}

	publicAssets, err := fs.Sub(webassets.Assets, "public")
	if err != nil {
		return nil, fmt.Errorf("load public assets: %w", err)
	}
	adminAssets, err := fs.Sub(webassets.Assets, "admin")
	if err != nil {
		return nil, fmt.Errorf("load admin assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", jsonHandler(func() any {
		return map[string]any{"status": "ok"}
	}))
	mux.HandleFunc("GET /health/ready", jsonHandler(func() any {
		return map[string]any{"status": "ready"}
	}))
	mux.HandleFunc("GET /api/v1/meta", jsonHandler(func() any {
		meta := map[string]any{
			"version":    options.Version,
			"serverTime": options.Now().UTC().Format(time.RFC3339),
		}
		if options.PublicURL != "" {
			meta["publicUrl"] = options.PublicURL
		}
		if options.Store != nil {
			if revision, err := options.Store.Revision(context.Background()); err == nil {
				meta["revision"] = revision
			}
		}
		return meta
	}))
	if options.LinuxDO != nil {
		mux.HandleFunc("GET /api/v1/auth/linuxdo", func(writer http.ResponseWriter, request *http.Request) {
			if err := options.LinuxDO.Begin(writer); err != nil {
				http.Error(writer, "LinuxDo 登录未配置", http.StatusNotImplemented)
			}
		})
		mux.HandleFunc("GET /api/v1/auth/linuxdo/callback", func(writer http.ResponseWriter, request *http.Request) {
			user, err := options.LinuxDO.Callback(request.Context(), request.URL.Query().Get("code"), request.URL.Query().Get("state"))
			if err != nil {
				http.Error(writer, "LinuxDo 登录失败", http.StatusBadRequest)
				return
			}
			token, expires, err := options.LinuxDO.StartSession(user)
			if err != nil {
				http.Error(writer, "创建登录会话失败", http.StatusInternalServerError)
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "relaypulse_user", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: request.TLS != nil, Expires: expires})
			http.Redirect(writer, request, "/", http.StatusFound)
		})
		mux.HandleFunc("GET /api/v1/auth/me", func(writer http.ResponseWriter, request *http.Request) {
			if user, ok := linuxDOUser(options.LinuxDO, request); ok {
				writeJSON(writer, map[string]any{"authenticated": true, "user": user})
				return
			}
			writeJSON(writer, map[string]any{"authenticated": false})
		})
		mux.HandleFunc("POST /api/v1/auth/logout", func(writer http.ResponseWriter, request *http.Request) {
			if cookie, err := request.Cookie("relaypulse_user"); err == nil {
				options.LinuxDO.Logout(cookie.Value)
			}
			http.SetCookie(writer, &http.Cookie{Name: "relaypulse_user", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: request.TLS != nil})
			writeJSON(writer, map[string]string{"status": "ok"})
		})
		mux.HandleFunc("POST /api/v1/feedback", func(writer http.ResponseWriter, request *http.Request) {
			user, ok := linuxDOUser(options.LinuxDO, request)
			if !ok {
				http.Error(writer, "请先使用 LinuxDo 登录", http.StatusUnauthorized)
				return
			}
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
				http.Error(writer, "反馈格式错误", http.StatusBadRequest)
				return
			}
			if err := options.Store.CreateFeedback(request.Context(), user.ID, payload.Content); err != nil {
				http.Error(writer, "反馈内容无效", http.StatusBadRequest)
				return
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		})
	}
	if options.Store != nil {
		for _, path := range []string{"/api/v1/session-sync/exchange", "/api/v1/session-sync/login", "/api/v1/session-sync/pending", "/api/v1/session-sync/batch"} {
			mux.HandleFunc("OPTIONS "+path, func(writer http.ResponseWriter, request *http.Request) {
				origin := request.Header.Get("Origin")
				if !session.ValidExtensionOrigin(origin) {
					http.Error(writer, "Chrome extension origin required", http.StatusForbidden)
					return
				}
				setExtensionCORS(writer, origin)
				writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-RelayPulse-Sync-Password")
				writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				writer.WriteHeader(http.StatusNoContent)
			})
		}
		mux.HandleFunc("POST /api/v1/session-sync/exchange", func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if !session.ValidExtensionOrigin(origin) {
				http.Error(writer, "Chrome extension origin required", http.StatusForbidden)
				return
			}
			setExtensionCORS(writer, origin)
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10)).Decode(&payload); err != nil {
				http.Error(writer, "invalid pairing payload", http.StatusBadRequest)
				return
			}
			token, expiresAt, err := options.SessionSync.Exchange(payload.Code, origin)
			if err != nil {
				if errors.Is(err, session.ErrPairingRateLimited) {
					http.Error(writer, "too many pairing attempts", http.StatusTooManyRequests)
					return
				}
				http.Error(writer, "invalid or expired pairing code", http.StatusUnauthorized)
				return
			}
			writeJSON(writer, map[string]any{"token": token, "expiresAt": expiresAt})
		})
		mux.HandleFunc("POST /api/v1/session-sync/login", func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if !session.ValidExtensionOrigin(origin) {
				http.Error(writer, "Chrome extension origin required", http.StatusForbidden)
				return
			}
			setExtensionCORS(writer, origin)
			var payload struct {
				Password string `json:"password"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
				http.Error(writer, "invalid login payload", http.StatusBadRequest)
				return
			}
			if _, ok := options.Auth.Login(request.RemoteAddr, payload.Password); !ok {
				http.Error(writer, "管理员密码错误", http.StatusUnauthorized)
				return
			}
			token, expiresAt, err := options.SessionSync.CreateToken(origin)
			if err != nil {
				http.Error(writer, "create session sync token", http.StatusInternalServerError)
				return
			}
			writeJSON(writer, map[string]any{"token": token, "expiresAt": expiresAt})
		})
		mux.HandleFunc("GET /api/v1/session-sync/pending", func(writer http.ResponseWriter, request *http.Request) {
			origin, _, _, ok := authorizeSessionSync(request, options.SessionSync, options.Auth)
			if session.ValidExtensionOrigin(origin) {
				setExtensionCORS(writer, origin)
			}
			if !ok {
				http.Error(writer, "invalid session sync token", http.StatusUnauthorized)
				return
			}
			items, err := pendingSessionSites(request.Context(), options.Store, options.Now())
			if err != nil {
				http.Error(writer, "query pending sessions", http.StatusInternalServerError)
				return
			}
			writeJSON(writer, map[string]any{"sites": items})
		})
		mux.HandleFunc("POST /api/v1/session-sync/pending", func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if session.ValidExtensionOrigin(origin) {
				setExtensionCORS(writer, origin)
			}
			var payload struct {
				Password string `json:"password"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil || options.Auth == nil || !options.Auth.VerifyPassword(payload.Password) {
				http.Error(writer, "invalid session sync credential", http.StatusUnauthorized)
				return
			}
			items, err := pendingSessionSites(request.Context(), options.Store, options.Now())
			if err != nil {
				http.Error(writer, "query pending sessions", http.StatusInternalServerError)
				return
			}
			writeJSON(writer, map[string]any{"sites": items})
		})
		mux.HandleFunc("POST /api/v1/session-sync/batch", func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if session.ValidExtensionOrigin(origin) {
				setExtensionCORS(writer, origin)
			}
			var payload struct {
				Password string              `json:"password"`
				Bundles  []sessionSyncBundle `json:"bundles"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 512<<10)).Decode(&payload); err != nil || len(payload.Bundles) == 0 || len(payload.Bundles) > 30 {
				http.Error(writer, "invalid session batch", http.StatusBadRequest)
				return
			}
			token, direct, ok := "", true, options.Auth != nil && options.Auth.VerifyPassword(payload.Password)
			if !ok {
				_, token, direct, ok = authorizeSessionSync(request, options.SessionSync, options.Auth)
			}
			if !ok || options.SessionVault == nil {
				http.Error(writer, "invalid session sync credential", http.StatusUnauthorized)
				return
			}
			pendingSites, err := pendingSessionSites(request.Context(), options.Store, options.Now())
			if err != nil {
				http.Error(writer, "query pending sessions", http.StatusInternalServerError)
				return
			}
			allowed := make(map[int64]sessionSyncSite, len(pendingSites))
			for _, site := range pendingSites {
				allowed[site.ID] = site
			}
			seen := make(map[int64]struct{}, len(payload.Bundles))
			for _, bundle := range payload.Bundles {
				site, exists := allowed[bundle.SiteID]
				if _, duplicate := seen[bundle.SiteID]; !exists || duplicate {
					http.Error(writer, "unknown site in session batch", http.StatusBadRequest)
					return
				}
				bundleOrigin, bundleOriginErr := session.NormalizeOrigin(bundle.Origin)
				if bundleOriginErr != nil || site.Origin != bundleOrigin {
					http.Error(writer, "site origin mismatch", http.StatusBadRequest)
					return
				}
				seen[bundle.SiteID] = struct{}{}
				if _, _, err := options.SessionVault.Encrypt(bundle.Data); err != nil {
					http.Error(writer, "invalid session in batch", http.StatusBadRequest)
					return
				}
			}
			if !direct && !options.SessionSync.Claim(token, origin) {
				http.Error(writer, "session sync token already in use", http.StatusUnauthorized)
				return
			}
			saved := false
			if !direct {
				defer func() { options.SessionSync.Finish(token, origin, saved) }()
			}
			items := make([]session.BatchItem, 0, len(payload.Bundles))
			for _, bundle := range payload.Bundles {
				items = append(items, session.BatchItem{SiteID: bundle.SiteID, Data: bundle.Data})
			}
			if err := options.SessionVault.SaveBatch(request.Context(), options.Store, items, nil); err != nil {
				http.Error(writer, "save session batch", http.StatusBadRequest)
				return
			}
			saved = true
			type verificationResult struct {
				SiteID int64  `json:"siteId"`
				Name   string `json:"name"`
				OK     bool   `json:"ok"`
				Error  string `json:"error,omitempty"`
			}
			results := make([]verificationResult, 0, len(payload.Bundles))
			for _, bundle := range payload.Bundles {
				result := verificationResult{SiteID: bundle.SiteID, Name: allowed[bundle.SiteID].Name}
				if options.Collector == nil {
					result.OK = true
				} else {
					collectCtx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
					err := options.Collector.CollectNow(collectCtx, bundle.SiteID)
					cancel()
					result.OK = err == nil
					if err != nil {
						result.Error = "采集验证失败"
					}
				}
				results = append(results, result)
			}
			writeJSON(writer, map[string]any{"status": "ok", "imported": len(payload.Bundles), "results": results})
		})
		dashboardCache := &publicDashboardCache{}
		mux.HandleFunc("GET /api/v1/public/dashboard", func(writer http.ResponseWriter, request *http.Request) {
			payload, err := dashboardCache.load(request.Context(), options.Store, options.Now())
			if err != nil {
				http.Error(writer, "query public dashboard", http.StatusInternalServerError)
				return
			}
			writeJSONBytes(writer, payload)
		})
		mux.HandleFunc("GET /api/v1/public/announcements", func(writer http.ResponseWriter, request *http.Request) {
			if options.Store == nil {
				writeJSON(writer, map[string]any{"announcements": []store.FailureAnnouncement{}})
				return
			}
			items, err := options.Store.ListActiveFailureAnnouncements(request.Context())
			if err != nil {
				http.Error(writer, "query public announcements", http.StatusInternalServerError)
				return
			}
			revision, _ := options.Store.Revision(request.Context())
			writeJSON(writer, map[string]any{"revision": revision, "announcements": items})
		})
		mux.HandleFunc("GET /api/v1/public/rows", func(writer http.ResponseWriter, request *http.Request) {
			rows, err := options.Store.QueryPublicRows(request.Context(), request.URL.Query().Get("model"), request.URL.Query().Get("site"))
			if err != nil {
				http.Error(writer, "query public rows", http.StatusInternalServerError)
				return
			}
			writeJSON(writer, map[string]any{"rows": rows})
		})
		mux.HandleFunc("GET /api/v1/public/details", func(writer http.ResponseWriter, request *http.Request) {
			siteName := request.URL.Query().Get("site")
			rawModel := request.URL.Query().Get("raw")
			if siteName == "" || rawModel == "" {
				http.Error(writer, "site and raw model are required", http.StatusBadRequest)
				return
			}
			hours := 24
			if raw := request.URL.Query().Get("hours"); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 && parsed <= 72 {
					hours = parsed
				}
			}
			items, err := options.Store.QueryPublicDetails(request.Context(), request.URL.Query().Get("model"), siteName, rawModel, options.Now().UTC().Add(-time.Duration(hours)*time.Hour))
			if err != nil {
				http.Error(writer, "query public details", http.StatusInternalServerError)
				return
			}
			groups, err := options.Store.QueryPublicDetailGroups(request.Context(), request.URL.Query().Get("model"), siteName, rawModel)
			if err != nil {
				http.Error(writer, "query public detail groups", http.StatusInternalServerError)
				return
			}
			writeJSON(writer, map[string]any{"buckets": items, "groups": groups, "hours": hours})
		})
		mux.HandleFunc("GET /api/v1/sites", func(writer http.ResponseWriter, request *http.Request) {
			sites, err := options.Store.ListAllSites(request.Context())
			if err != nil {
				http.Error(writer, "query sites", http.StatusInternalServerError)
				return
			}
			type publicSite struct {
				ID               int64                   `json:"id"`
				Name             string                  `json:"name"`
				SourceURL        string                  `json:"sourceUrl"`
				Enabled          bool                    `json:"enabled"`
				AcquisitionState domain.AcquisitionState `json:"acquisitionState"`
			}
			public := make([]publicSite, 0, len(sites))
			for _, site := range sites {
				public = append(public, publicSite{ID: site.ID, Name: site.Name, SourceURL: site.SourceURL, Enabled: site.Enabled, AcquisitionState: site.AcquisitionState})
			}
			writeJSON(writer, map[string]any{"sites": public})
		})
		if options.Auth != nil {
			mux.HandleFunc("POST /api/v1/admin/login", func(writer http.ResponseWriter, request *http.Request) {
				var payload struct {
					Password string `json:"password"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
					http.Error(writer, "invalid login payload", http.StatusBadRequest)
					return
				}
				remoteHost, _, splitErr := net.SplitHostPort(request.RemoteAddr)
				if splitErr != nil {
					remoteHost = request.RemoteAddr
				}
				token, ok := options.Auth.Login(remoteHost, payload.Password)
				if !ok {
					http.Error(writer, "管理员密码错误", http.StatusUnauthorized)
					return
				}
				http.SetCookie(writer, &http.Cookie{Name: "relaypulse_admin", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: request.TLS != nil, MaxAge: 12 * 60 * 60})
				http.SetCookie(writer, &http.Cookie{Name: "relaypulse_csrf", Value: token, Path: "/", HttpOnly: false, SameSite: http.SameSiteStrictMode, Secure: request.TLS != nil, MaxAge: 12 * 60 * 60})
				writeJSON(writer, map[string]string{"status": "ok"})
			})
			adminHandler := options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				sites, err := options.Store.ListAllSites(request.Context())
				if err != nil {
					http.Error(writer, "query sites", http.StatusInternalServerError)
					return
				}
				writeJSON(writer, map[string]any{"sites": sites})
			}))
			mux.Handle("GET /api/v1/admin/sites", adminHandler)
			mux.Handle("POST /api/v1/admin/session-sync/pair", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if options.SessionVault == nil {
					http.Error(writer, "session vault unavailable", http.StatusNotImplemented)
					return
				}
				code, expiresAt, err := options.SessionSync.CreatePairing()
				if err != nil {
					http.Error(writer, "create pairing code", http.StatusInternalServerError)
					return
				}
				writeJSON(writer, map[string]any{"code": code, "expiresAt": expiresAt})
			}))))
			mux.Handle("POST /api/v1/admin/sites", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var payload store.Site
				if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&payload); err != nil {
					http.Error(writer, "invalid site payload", http.StatusBadRequest)
					return
				}
				payload.Interval = time.Duration(payload.IntervalSeconds) * time.Second
				payload.Jitter = time.Duration(payload.JitterSeconds) * time.Second
				created, err := options.Store.CreateManagedSite(request.Context(), payload)
				if err != nil {
					http.Error(writer, "create site", http.StatusBadRequest)
					return
				}
				writeJSON(writer, map[string]any{"status": "ok", "site": created})
			}))))
			mux.Handle("PATCH /api/v1/admin/sites/{id}", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
				if err != nil {
					http.Error(writer, "invalid site id", http.StatusBadRequest)
					return
				}
				var payload struct {
					Name                string  `json:"name"`
					AdapterKey          string  `json:"adapterKey"`
					AdapterConfig       string  `json:"adapterConfig"`
					Enabled             bool    `json:"enabled"`
					SessionRequired     *bool   `json:"sessionRequired"`
					CustomFailureReason *string `json:"customFailureReason"`
					IntervalSeconds     int64   `json:"intervalSeconds"`
					JitterSeconds       int64   `json:"jitterSeconds"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&payload); err != nil {
					http.Error(writer, "invalid site payload", http.StatusBadRequest)
					return
				}
				if err := options.Store.UpdateSite(request.Context(), id, payload.Name, payload.AdapterKey, payload.AdapterConfig, payload.Enabled, payload.SessionRequired, time.Duration(payload.IntervalSeconds)*time.Second, time.Duration(payload.JitterSeconds)*time.Second); err != nil {
					http.Error(writer, "update site", http.StatusBadRequest)
					return
				}
				if payload.CustomFailureReason != nil {
					if err := options.Store.UpdateSiteFailureReason(request.Context(), id, *payload.CustomFailureReason); err != nil {
						http.Error(writer, "update site failure reason", http.StatusBadRequest)
						return
					}
				}
				writeJSON(writer, map[string]string{"status": "ok"})
			}))))
			mux.Handle("POST /api/v1/admin/sites/{id}/collect", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if options.Collector == nil {
					http.Error(writer, "collector unavailable", http.StatusServiceUnavailable)
					return
				}
				id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
				if err != nil {
					http.Error(writer, "invalid site id", http.StatusBadRequest)
					return
				}
				collectCtx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
				defer cancel()
				if err := options.Collector.CollectNow(collectCtx, id); err != nil {
					http.Error(writer, "collection failed", http.StatusBadGateway)
					return
				}
				writeJSON(writer, map[string]string{"status": "ok"})
			}))))
			mux.Handle("POST /api/v1/admin/sites/{id}/session", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if options.SessionVault == nil {
					http.Error(writer, "session vault unavailable", http.StatusNotImplemented)
					return
				}
				id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
				if err != nil {
					http.Error(writer, "invalid site id", http.StatusBadRequest)
					return
				}
				var payload session.Data
				if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&payload); err != nil {
					http.Error(writer, "invalid session payload", http.StatusBadRequest)
					return
				}
				if err := options.SessionVault.Save(request.Context(), options.Store, id, payload, nil); err != nil {
					http.Error(writer, "save session", http.StatusBadRequest)
					return
				}
				writeJSON(writer, map[string]string{"status": "ok"})
			}))))
			mux.Handle("DELETE /api/v1/admin/sites/{id}/session", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if options.SessionVault == nil {
					http.Error(writer, "session vault unavailable", http.StatusNotImplemented)
					return
				}
				id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
				if err != nil {
					http.Error(writer, "invalid site id", http.StatusBadRequest)
					return
				}
				if err := options.Store.DeleteEncryptedSession(request.Context(), id, session.SessionPurpose); err != nil {
					http.Error(writer, "delete session", http.StatusInternalServerError)
					return
				}
				writeJSON(writer, map[string]string{"status": "ok"})
			}))))
			mux.Handle("GET /api/v1/admin/adapters", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if options.Collector == nil {
					writeJSON(writer, map[string]any{"adapters": []any{}})
					return
				}
				items := make([]map[string]any, 0)
				for _, item := range options.Collector.Registry().List() {
					items = append(items, map[string]any{"key": item.Key(), "displayName": item.DisplayName(), "configSchema": item.ConfigSchema()})
				}
				writeJSON(writer, map[string]any{"adapters": items})
			})))
			mux.Handle("GET /api/v1/admin/rules", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				rules, err := options.Store.ListRules(request.Context())
				if err != nil {
					http.Error(writer, "query rules", http.StatusInternalServerError)
					return
				}
				writeJSON(writer, map[string]any{"rules": rules})
			})))
			mux.Handle("GET /api/v1/admin/runs", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				runs, err := options.Store.ListRecentCollectionRunsBySite(request.Context(), 12)
				if err != nil {
					http.Error(writer, "query collection runs", http.StatusInternalServerError)
					return
				}
				writeJSON(writer, map[string]any{"runs": runs})
			})))
			mux.Handle("GET /api/v1/admin/conflicts", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				conflicts, err := options.Store.ListMatchConflicts(request.Context(), 300)
				if err != nil {
					http.Error(writer, "query match conflicts", http.StatusInternalServerError)
					return
				}
				writeJSON(writer, map[string]any{"conflicts": conflicts})
			})))
			mux.Handle("GET /api/v1/admin/feedback", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				items, err := options.Store.ListFeedback(request.Context(), 200)
				if err != nil {
					http.Error(writer, "query feedback", http.StatusInternalServerError)
					return
				}
				writeJSON(writer, map[string]any{"feedback": items})
			})))
			mux.Handle("POST /api/v1/admin/rules/preview", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var rule matcher.Rule
				if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&rule); err != nil {
					http.Error(writer, "invalid rule payload", http.StatusBadRequest)
					return
				}
				matches, err := options.Store.PreviewRule(request.Context(), rule, 500)
				if err != nil {
					http.Error(writer, "preview rule", http.StatusBadRequest)
					return
				}
				writeJSON(writer, map[string]any{"matches": matches})
			}))))
			mux.Handle("POST /api/v1/admin/rules", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var rule matcher.Rule
				if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&rule); err != nil {
					http.Error(writer, "invalid rule payload", http.StatusBadRequest)
					return
				}
				if err := options.Store.CreateRule(request.Context(), rule); err != nil {
					http.Error(writer, "create rule", http.StatusBadRequest)
					return
				}
				if options.Collector != nil {
					if err := options.Collector.ReloadMatcher(request.Context()); err != nil {
						http.Error(writer, "reload matcher", http.StatusInternalServerError)
						return
					}
				}
				writeJSON(writer, map[string]string{"status": "ok"})
			}))))
			mux.Handle("PUT /api/v1/admin/rules/{id}", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
				if err != nil {
					http.Error(writer, "invalid rule id", http.StatusBadRequest)
					return
				}
				var rule matcher.Rule
				if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&rule); err != nil {
					http.Error(writer, "invalid rule payload", http.StatusBadRequest)
					return
				}
				if err := options.Store.UpdateRule(request.Context(), id, rule); err != nil {
					http.Error(writer, "update rule", http.StatusBadRequest)
					return
				}
				if options.Collector != nil {
					if err := options.Collector.ReloadMatcher(request.Context()); err != nil {
						http.Error(writer, "reload matcher", http.StatusInternalServerError)
						return
					}
				}
				writeJSON(writer, map[string]string{"status": "ok"})
			}))))
			mux.Handle("DELETE /api/v1/admin/rules/{id}", options.Auth.Middleware(csrfMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
				if err != nil {
					http.Error(writer, "invalid rule id", http.StatusBadRequest)
					return
				}
				if err := options.Store.DeleteRule(request.Context(), id); err != nil {
					http.Error(writer, "delete rule", http.StatusBadRequest)
					return
				}
				if options.Collector != nil {
					if err := options.Collector.ReloadMatcher(request.Context()); err != nil {
						http.Error(writer, "reload matcher", http.StatusInternalServerError)
						return
					}
				}
				writeJSON(writer, map[string]string{"status": "ok"})
			}))))
		}
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(publicAssets))))
	mux.Handle("GET /admin/", noStore(http.StripPrefix("/admin/", http.FileServer(http.FS(adminAssets)))))
	mux.Handle("GET /", http.FileServer(http.FS(publicAssets)))

	return requestLogger(options.Logger, securityHeaders(mux)), nil
}

func jsonHandler(value func() any) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, value())
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(value)
}

func writeJSONBytes(writer http.ResponseWriter, payload []byte) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write(payload)
}

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

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(writer, request)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(writer, request)
	})
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		logger.Debug("http request",
			"method", request.Method,
			"path", request.URL.Path,
			"duration", time.Since(started),
		)
	})
}

func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-CSRF-Token") == "" {
			http.Error(writer, "CSRF token required", http.StatusForbidden)
			return
		}
		cookie, err := request.Cookie("relaypulse_csrf")
		if err != nil || cookie.Value == "" || cookie.Value != request.Header.Get("X-CSRF-Token") {
			http.Error(writer, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func authorizeSessionSync(request *http.Request, manager *session.SyncManager, auth *admin.Auth) (string, string, bool, bool) {
	origin := request.Header.Get("Origin")
	if session.ValidExtensionOrigin(origin) && auth != nil && auth.VerifyPassword(request.Header.Get("X-RelayPulse-Sync-Password")) {
		return origin, "", true, true
	}
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		return origin, "", false, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	return origin, token, false, manager != nil && manager.Authorize(token, origin)
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
