package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"relaypulse/internal/domain"
	"relaypulse/internal/session"
	"relaypulse/internal/store"
	webassets "relaypulse/web"
)

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
		if options.LinuxDO != nil && options.LinuxDO.Enabled() {
			meta["authProviders"] = []string{"linuxdo"}
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
				writeError(writer, http.StatusNotImplemented, "OAuth login is not configured")
			}
		})
		mux.HandleFunc("GET /api/v1/auth/linuxdo/callback", func(writer http.ResponseWriter, request *http.Request) {
			user, err := options.LinuxDO.Callback(request.Context(), request.URL.Query().Get("code"), request.URL.Query().Get("state"))
			if err != nil {
				writeError(writer, http.StatusBadRequest, "登录失败")
				return
			}
			token, expires, err := options.LinuxDO.StartSession(user)
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "创建登录会话失败")
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
				writeError(writer, http.StatusUnauthorized, "请先登录")
				return
			}
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
				writeError(writer, http.StatusBadRequest, "反馈格式错误")
				return
			}
			if err := options.Store.CreateFeedback(request.Context(), user.ID, payload.Content); err != nil {
				writeError(writer, http.StatusBadRequest, "反馈内容无效")
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
					writeError(writer, http.StatusForbidden, "Chrome extension origin required")
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
				writeError(writer, http.StatusForbidden, "Chrome extension origin required")
				return
			}
			setExtensionCORS(writer, origin)
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10)).Decode(&payload); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid pairing payload")
				return
			}
			token, expiresAt, err := options.SessionSync.Exchange(payload.Code, origin)
			if err != nil {
				if errors.Is(err, session.ErrPairingRateLimited) {
					writeError(writer, http.StatusTooManyRequests, "too many pairing attempts")
					return
				}
				writeError(writer, http.StatusUnauthorized, "invalid or expired pairing code")
				return
			}
			writeJSON(writer, map[string]any{"token": token, "expiresAt": expiresAt})
		})
		mux.HandleFunc("POST /api/v1/session-sync/login", func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if !session.ValidExtensionOrigin(origin) {
				writeError(writer, http.StatusForbidden, "Chrome extension origin required")
				return
			}
			setExtensionCORS(writer, origin)
			var payload struct {
				Password string `json:"password"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid login payload")
				return
			}
			if _, ok := options.Auth.Login(request.RemoteAddr, payload.Password); !ok {
				writeError(writer, http.StatusUnauthorized, "管理员密码错误")
				return
			}
			token, expiresAt, err := options.SessionSync.CreateToken(origin)
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "create session sync token")
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
				writeError(writer, http.StatusUnauthorized, "invalid session sync token")
				return
			}
			items, err := pendingSessionSites(request.Context(), options.Store, options.Now())
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query pending sessions")
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
				writeError(writer, http.StatusUnauthorized, "invalid session sync credential")
				return
			}
			items, err := pendingSessionSites(request.Context(), options.Store, options.Now())
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query pending sessions")
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
				writeError(writer, http.StatusBadRequest, "invalid session batch")
				return
			}
			token, direct, ok := "", true, options.Auth != nil && options.Auth.VerifyPassword(payload.Password)
			if !ok {
				_, token, direct, ok = authorizeSessionSync(request, options.SessionSync, options.Auth)
			}
			if !ok || options.SessionVault == nil {
				writeError(writer, http.StatusUnauthorized, "invalid session sync credential")
				return
			}
			pendingSites, err := pendingSessionSites(request.Context(), options.Store, options.Now())
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query pending sessions")
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
					writeError(writer, http.StatusBadRequest, "unknown site in session batch")
					return
				}
				bundleOrigin, bundleOriginErr := session.NormalizeOrigin(bundle.Origin)
				if bundleOriginErr != nil || site.Origin != bundleOrigin {
					writeError(writer, http.StatusBadRequest, "site origin mismatch")
					return
				}
				seen[bundle.SiteID] = struct{}{}
				if _, _, err := options.SessionVault.Encrypt(bundle.Data); err != nil {
					writeError(writer, http.StatusBadRequest, "invalid session in batch")
					return
				}
			}
			if !direct && !options.SessionSync.Claim(token, origin) {
				writeError(writer, http.StatusUnauthorized, "session sync token already in use")
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
				writeError(writer, http.StatusBadRequest, "save session batch")
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
				writeError(writer, http.StatusInternalServerError, "query public dashboard")
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
				writeError(writer, http.StatusInternalServerError, "query public announcements")
				return
			}
			revision, _ := options.Store.Revision(request.Context())
			writeJSON(writer, map[string]any{"revision": revision, "announcements": items})
		})
		mux.HandleFunc("GET /api/v1/public/rows", func(writer http.ResponseWriter, request *http.Request) {
			rows, err := options.Store.QueryPublicRows(request.Context(), request.URL.Query().Get("model"), request.URL.Query().Get("site"))
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query public rows")
				return
			}
			writeJSON(writer, map[string]any{"rows": rows})
		})
		mux.HandleFunc("GET /api/v1/public/details", func(writer http.ResponseWriter, request *http.Request) {
			siteName := request.URL.Query().Get("site")
			rawModel := request.URL.Query().Get("raw")
			if siteName == "" || rawModel == "" {
				writeError(writer, http.StatusBadRequest, "site and raw model are required")
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
				writeError(writer, http.StatusInternalServerError, "query public details")
				return
			}
			groups, err := options.Store.QueryPublicDetailGroups(request.Context(), request.URL.Query().Get("model"), siteName, rawModel)
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query public detail groups")
				return
			}
			writeJSON(writer, map[string]any{"buckets": items, "groups": groups, "hours": hours})
		})
		mux.HandleFunc("GET /api/v1/sites", func(writer http.ResponseWriter, request *http.Request) {
			sites, err := options.Store.ListAllSites(request.Context())
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query sites")
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
		registerAdminRoutes(mux, options)
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(publicAssets))))
	mux.Handle("GET /admin/", noStore(http.StripPrefix("/admin/", http.FileServer(http.FS(adminAssets)))))
	mux.Handle("GET /", http.FileServer(http.FS(publicAssets)))

	return requestLogger(options.Logger, securityHeaders(mux)), nil
}
