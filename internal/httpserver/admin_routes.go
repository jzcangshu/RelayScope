package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"relayscope/internal/matcher"
	"relayscope/internal/session"
	"relayscope/internal/store"
)

// manualCollectionTimeout mirrors scheduler.scheduledCollectionTimeout so
// on-demand collection honors the same ceiling as scheduled runs. Keep the
// two in sync; challenge-protected newapi-pricing sites solve the Cloudflare
// challenge twice per collection and need headroom beyond a single solve.
const manualCollectionTimeout = 7 * time.Minute

func registerAdminRoutes(mux *http.ServeMux, options Options) {
	if options.Auth != nil {
		mux.HandleFunc("POST /api/v1/admin/login", func(writer http.ResponseWriter, request *http.Request) {
			var payload struct {
				Password string `json:"password"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid login payload")
				return
			}
			remoteHost, _, splitErr := net.SplitHostPort(request.RemoteAddr)
			if splitErr != nil {
				remoteHost = request.RemoteAddr
			}
			token, ok := options.Auth.Login(remoteHost, payload.Password)
			if !ok {
				writeError(writer, http.StatusUnauthorized, "管理员密码错误")
				return
			}
			csrfToken, ok := options.Auth.NewCSRFToken(token)
			if !ok {
				writeError(writer, http.StatusInternalServerError, "create CSRF token")
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "relayscope_admin", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: request.TLS != nil, MaxAge: 12 * 60 * 60})
			http.SetCookie(writer, &http.Cookie{Name: "relayscope_csrf", Value: csrfToken, Path: "/", HttpOnly: false, SameSite: http.SameSiteStrictMode, Secure: request.TLS != nil, MaxAge: 12 * 60 * 60})
			writeJSON(writer, map[string]string{"status": "ok"})
		})
		mux.Handle("POST /api/v1/admin/logout", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if cookie, err := request.Cookie("relayscope_admin"); err == nil {
				options.Auth.Logout(cookie.Value)
			}
			http.SetCookie(writer, &http.Cookie{Name: "relayscope_admin", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: request.TLS != nil})
			http.SetCookie(writer, &http.Cookie{Name: "relayscope_csrf", Value: "", Path: "/", MaxAge: -1, SameSite: http.SameSiteStrictMode, Secure: request.TLS != nil})
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		adminHandler := options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			sites, err := options.Store.ListAllSites(request.Context())
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query sites")
				return
			}
			writeJSON(writer, map[string]any{"sites": sites})
		}))
		mux.Handle("GET /api/v1/admin/sites", adminHandler)
		mux.Handle("POST /api/v1/admin/session-sync/pair", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if options.SessionVault == nil {
				writeError(writer, http.StatusNotImplemented, "session vault unavailable")
				return
			}
			code, expiresAt, err := options.SessionSync.CreatePairing()
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "create pairing code")
				return
			}
			writeJSON(writer, map[string]any{"code": code, "expiresAt": expiresAt})
		}))))
		mux.Handle("POST /api/v1/admin/sites", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var payload store.Site
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&payload); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site payload")
				return
			}
			payload.Interval = time.Duration(payload.IntervalSeconds) * time.Second
			payload.Jitter = time.Duration(payload.JitterSeconds) * time.Second
			if !adapterRegistered(options, payload.AdapterKey) {
				writeError(writer, http.StatusBadRequest, "unknown adapter")
				return
			}
			created, err := options.Store.CreateManagedSite(request.Context(), payload)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "create site")
				return
			}
			writeJSON(writer, map[string]any{"status": "ok", "site": created})
		}))))
		mux.Handle("PATCH /api/v1/admin/sites/{id}", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site id")
				return
			}
			var payload struct {
				Name                string  `json:"name"`
				BaseURL             string  `json:"baseUrl"`
				SourceURL           string  `json:"sourceUrl"`
				AdapterKey          string  `json:"adapterKey"`
				AdapterConfig       string  `json:"adapterConfig"`
				Enabled             bool    `json:"enabled"`
				SessionRequired     *bool   `json:"sessionRequired"`
				CustomFailureReason *string `json:"customFailureReason"`
				IntervalSeconds     int64   `json:"intervalSeconds"`
				JitterSeconds       int64   `json:"jitterSeconds"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&payload); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site payload")
				return
			}
			if !adapterRegistered(options, payload.AdapterKey) {
				writeError(writer, http.StatusBadRequest, "unknown adapter")
				return
			}
			current, err := options.Store.GetSite(request.Context(), id)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "update site")
				return
			}
			baseURL, sourceURL := payload.BaseURL, payload.SourceURL
			if strings.TrimSpace(baseURL) == "" {
				baseURL = current.BaseURL
			}
			if strings.TrimSpace(sourceURL) == "" {
				sourceURL = current.SourceURL
			}
			failureReason := current.CustomFailureReason
			if payload.CustomFailureReason != nil {
				failureReason = *payload.CustomFailureReason
			}
			if err := options.Store.UpdateSiteDetails(request.Context(), id, payload.Name, baseURL, sourceURL, payload.AdapterKey, payload.AdapterConfig, payload.Enabled, valueOrBool(payload.SessionRequired, current.SessionRequired), time.Duration(payload.IntervalSeconds)*time.Second, time.Duration(payload.JitterSeconds)*time.Second, failureReason); err != nil {
				writeError(writer, http.StatusBadRequest, "update site")
				return
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		mux.Handle("DELETE /api/v1/admin/sites/{id}", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site id")
				return
			}
			if err := options.Store.DeleteSite(request.Context(), id, options.Now().UTC()); err != nil {
				writeError(writer, http.StatusBadRequest, "delete site")
				return
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		mux.Handle("POST /api/v1/admin/sites/{id}/restore", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site id")
				return
			}
			if err := options.Store.RestoreSite(request.Context(), id, options.Now().UTC()); err != nil {
				writeError(writer, http.StatusBadRequest, "restore site")
				return
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		mux.Handle("POST /api/v1/admin/sites/{id}/collect", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if options.Collector == nil {
				writeError(writer, http.StatusServiceUnavailable, "collector unavailable")
				return
			}
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site id")
				return
			}
			collectCtx, cancel := context.WithTimeout(request.Context(), manualCollectionTimeout)
			defer cancel()
			if err := options.Collector.CollectNow(collectCtx, id); err != nil {
				writeError(writer, http.StatusBadGateway, "collection failed")
				return
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		mux.Handle("POST /api/v1/admin/sites/{id}/session", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if options.SessionVault == nil {
				writeError(writer, http.StatusNotImplemented, "session vault unavailable")
				return
			}
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site id")
				return
			}
			if _, err := options.Store.GetSite(request.Context(), id); err != nil {
				writeError(writer, http.StatusNotFound, "site not found")
				return
			}
			var payload session.Data
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&payload); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid session payload")
				return
			}
			if err := options.SessionVault.Save(request.Context(), options.Store, id, payload, nil); err != nil {
				writeError(writer, http.StatusBadRequest, "save session")
				return
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		mux.Handle("DELETE /api/v1/admin/sites/{id}/session", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if options.SessionVault == nil {
				writeError(writer, http.StatusNotImplemented, "session vault unavailable")
				return
			}
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site id")
				return
			}
			if _, err := options.Store.GetSite(request.Context(), id); err != nil {
				writeError(writer, http.StatusNotFound, "site not found")
				return
			}
			if err := options.Store.DeleteEncryptedSession(request.Context(), id, session.SessionPurpose); err != nil {
				writeError(writer, http.StatusInternalServerError, "delete session")
				return
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		mux.Handle("GET /api/v1/admin/sites/{id}/session", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid site id")
				return
			}
			if _, err := options.Store.GetSite(request.Context(), id); err != nil {
				writeError(writer, http.StatusNotFound, "site not found")
				return
			}
			metadata, err := options.Store.SessionMetadata(request.Context(), id, session.SessionPurpose)
			if err != nil {
				writeError(writer, http.StatusNotFound, "session metadata unavailable")
				return
			}
			response := map[string]any{"metadata": metadata}
			if options.SessionVault != nil {
				encrypted, loadErr := options.Store.LoadEncryptedSession(request.Context(), id, session.SessionPurpose)
				if loadErr == nil {
					data, decryptErr := options.SessionVault.Decrypt(encrypted.Nonce, encrypted.Ciphertext)
					if decryptErr == nil {
						response["credential"] = session.Describe(data)
					}
				}
			}
			writeJSON(writer, response)
		})))
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
				writeError(writer, http.StatusInternalServerError, "query rules")
				return
			}
			writeJSON(writer, map[string]any{"rules": rules})
		})))
		mux.Handle("GET /api/v1/admin/runs", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			filters, err := parseRunFilters(request)
			if err != nil {
				writeError(writer, http.StatusBadRequest, err.Error())
				return
			}
			runs, err := options.Store.ListCollectionRunsFiltered(request.Context(), filters)
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query collection runs")
				return
			}
			writeJSON(writer, map[string]any{"runs": runs})
		})))
		mux.Handle("GET /api/v1/admin/unmatched", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			limit := 100
			if raw := request.URL.Query().Get("limit"); raw != "" {
				parsed, err := strconv.Atoi(raw)
				if err != nil || parsed < 1 || parsed > 500 {
					writeError(writer, http.StatusBadRequest, "invalid limit")
					return
				}
				limit = parsed
			}
			items, err := options.Store.ListUnmatchedModels(request.Context(), limit)
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query unmatched models")
				return
			}
			writeJSON(writer, map[string]any{"models": items})
		})))
		mux.Handle("GET /api/v1/admin/conflicts", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			conflicts, err := options.Store.ListMatchConflicts(request.Context(), 300)
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query match conflicts")
				return
			}
			writeJSON(writer, map[string]any{"conflicts": conflicts})
		})))
		mux.Handle("GET /api/v1/admin/feedback", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			items, err := options.Store.ListFeedback(request.Context(), 200)
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query feedback")
				return
			}
			writeJSON(writer, map[string]any{"feedback": items})
		})))
		mux.Handle("POST /api/v1/admin/rules/preview", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var rule matcher.Rule
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&rule); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid rule payload")
				return
			}
			matches, err := options.Store.PreviewRule(request.Context(), rule, 500)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "preview rule")
				return
			}
			writeJSON(writer, map[string]any{"matches": matches})
		}))))
		mux.Handle("POST /api/v1/admin/rules", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var rule matcher.Rule
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&rule); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid rule payload")
				return
			}
			if err := options.Store.CreateRule(request.Context(), rule); err != nil {
				writeError(writer, http.StatusBadRequest, "create rule")
				return
			}
			if options.Collector != nil {
				if err := options.Collector.ReloadMatcher(request.Context()); err != nil {
					writeError(writer, http.StatusInternalServerError, "reload matcher")
					return
				}
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		mux.Handle("PUT /api/v1/admin/rules/{id}", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid rule id")
				return
			}
			var rule matcher.Rule
			if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10)).Decode(&rule); err != nil {
				writeError(writer, http.StatusBadRequest, "invalid rule payload")
				return
			}
			if err := options.Store.UpdateRule(request.Context(), id, rule); err != nil {
				writeError(writer, http.StatusBadRequest, "update rule")
				return
			}
			if options.Collector != nil {
				if err := options.Collector.ReloadMatcher(request.Context()); err != nil {
					writeError(writer, http.StatusInternalServerError, "reload matcher")
					return
				}
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
		mux.Handle("DELETE /api/v1/admin/rules/{id}", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "invalid rule id")
				return
			}
			if err := options.Store.DeleteRule(request.Context(), id); err != nil {
				writeError(writer, http.StatusBadRequest, "delete rule")
				return
			}
			if options.Collector != nil {
				if err := options.Collector.ReloadMatcher(request.Context()); err != nil {
					writeError(writer, http.StatusInternalServerError, "reload matcher")
					return
				}
			}
			writeJSON(writer, map[string]string{"status": "ok"})
		}))))
	}
}

func valueOrBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func adapterRegistered(options Options, key string) bool {
	if options.Collector == nil {
		return true
	}
	_, ok := options.Collector.Registry().Get(strings.TrimSpace(key))
	return ok
}

func parseRunFilters(request *http.Request) (store.RunFilters, error) {
	query := request.URL.Query()
	filters := store.RunFilters{Limit: 50}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return store.RunFilters{}, fmt.Errorf("invalid limit")
		}
		filters.Limit = limit
	}
	if raw := query.Get("site"); raw != "" {
		siteID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || siteID <= 0 {
			return store.RunFilters{}, fmt.Errorf("invalid site")
		}
		filters.SiteID = siteID
	}
	if status := strings.TrimSpace(query.Get("status")); status != "" {
		switch status {
		case "running", "success", "partial", "failed":
			filters.Status = status
		default:
			return store.RunFilters{}, fmt.Errorf("invalid status")
		}
	}
	if raw := strings.TrimSpace(query.Get("since")); raw != "" {
		since, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return store.RunFilters{}, fmt.Errorf("invalid since")
		}
		filters.Since = &since
	}
	return filters, nil
}
