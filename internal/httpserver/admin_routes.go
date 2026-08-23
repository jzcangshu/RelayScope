package httpserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"time"

	"relaypulse/internal/matcher"
	"relaypulse/internal/session"
	"relaypulse/internal/store"
)

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
			http.SetCookie(writer, &http.Cookie{Name: "relaypulse_admin", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: request.TLS != nil, MaxAge: 12 * 60 * 60})
			http.SetCookie(writer, &http.Cookie{Name: "relaypulse_csrf", Value: csrfToken, Path: "/", HttpOnly: false, SameSite: http.SameSiteStrictMode, Secure: request.TLS != nil, MaxAge: 12 * 60 * 60})
			writeJSON(writer, map[string]string{"status": "ok"})
		})
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
			if err := options.Store.UpdateSite(request.Context(), id, payload.Name, payload.AdapterKey, payload.AdapterConfig, payload.Enabled, payload.SessionRequired, time.Duration(payload.IntervalSeconds)*time.Second, time.Duration(payload.JitterSeconds)*time.Second); err != nil {
				writeError(writer, http.StatusBadRequest, "update site")
				return
			}
			if payload.CustomFailureReason != nil {
				if err := options.Store.UpdateSiteFailureReason(request.Context(), id, *payload.CustomFailureReason); err != nil {
					writeError(writer, http.StatusBadRequest, "update site failure reason")
					return
				}
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
			collectCtx, cancel := context.WithTimeout(request.Context(), 90*time.Second)
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
			if err := options.Store.DeleteEncryptedSession(request.Context(), id, session.SessionPurpose); err != nil {
				writeError(writer, http.StatusInternalServerError, "delete session")
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
				writeError(writer, http.StatusInternalServerError, "query rules")
				return
			}
			writeJSON(writer, map[string]any{"rules": rules})
		})))
		mux.Handle("GET /api/v1/admin/runs", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			runs, err := options.Store.ListRecentCollectionRunsBySite(request.Context(), 12)
			if err != nil {
				writeError(writer, http.StatusInternalServerError, "query collection runs")
				return
			}
			writeJSON(writer, map[string]any{"runs": runs})
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
