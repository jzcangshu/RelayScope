package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"relaypulse/internal/admin"
)

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

func csrfMiddleware(auth *admin.Auth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		csrfToken := request.Header.Get("X-CSRF-Token")
		adminCookie, adminErr := request.Cookie("relaypulse_admin")
		if auth == nil || adminErr != nil || csrfToken == "" {
			writeError(writer, http.StatusForbidden, "CSRF token required")
			return
		}
		cookie, err := request.Cookie("relaypulse_csrf")
		issuedToken, issued := auth.CSRFToken(adminCookie.Value)
		if err != nil || cookie.Value == "" || cookie.Value != csrfToken || !issued || issuedToken != csrfToken {
			writeError(writer, http.StatusForbidden, "invalid CSRF token")
			return
		}
		next.ServeHTTP(writer, request)
	})
}
