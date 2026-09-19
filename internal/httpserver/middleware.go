package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"relayscope/internal/admin"
)

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(writer, request)
	})
}

// cache5min 允许 CDN 缓存5分钟后回源，适用于静态资源。
func cache5min(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "public, max-age=300")
		next.ServeHTTP(writer, request)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// style-src 需 'unsafe-inline'：前端模板靠内联 style 渲染进度条 scaleX/入场 stagger，
		// 缺失时 CSP 会静默丢弃 style 属性（本地无 CSP 头所以测不出来）。
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
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
		adminCookie, adminErr := request.Cookie("relayscope_admin")
		if auth == nil || adminErr != nil || csrfToken == "" {
			writeError(writer, http.StatusForbidden, "CSRF token required")
			return
		}
		cookie, err := request.Cookie("relayscope_csrf")
		issuedToken, issued := auth.CSRFToken(adminCookie.Value)
		if err != nil || cookie.Value == "" || cookie.Value != csrfToken || !issued || issuedToken != csrfToken {
			writeError(writer, http.StatusForbidden, "invalid CSRF token")
			return
		}
		next.ServeHTTP(writer, request)
	})
}
