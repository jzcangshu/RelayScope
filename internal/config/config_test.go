package config

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("unexpected listen address: %q", cfg.ListenAddr)
	}
	if cfg.DataDir != "data" {
		t.Fatalf("unexpected data directory: %q", cfg.DataDir)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected log level: %v", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("unexpected shutdown timeout: %v", cfg.ShutdownTimeout)
	}
	if cfg.HTTPConcurrency != 3 || cfg.CollectionTimeout != 3*time.Minute || cfg.HTTPTimeout != 20*time.Second || cfg.MaintenanceInterval != 30*time.Minute {
		t.Fatalf("unexpected operational defaults: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"RELAYPULSE_LISTEN_ADDR":          "localhost:9090",
		"RELAYPULSE_DATA_DIR":             "var/state",
		"RELAYPULSE_LOG_LEVEL":            "debug",
		"RELAYPULSE_SHUTDOWN_TIMEOUT":     "3s",
		"RELAYPULSE_PUBLIC_URL":           "https://status.example.com/",
		"RELAYPULSE_HTTP_CONCURRENCY":     "5",
		"RELAYPULSE_COLLECTION_TIMEOUT":   "90s",
		"RELAYPULSE_HTTP_TIMEOUT":         "12s",
		"RELAYPULSE_MAINTENANCE_INTERVAL": "2h",
	}
	cfg, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}

	if cfg.ListenAddr != "localhost:9090" || cfg.DataDir != filepath.Clean("var/state") {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("typed overrides not applied: %+v", cfg)
	}
	if cfg.PublicURL != "https://status.example.com" {
		t.Fatalf("public URL = %q", cfg.PublicURL)
	}
	if cfg.HTTPConcurrency != 5 || cfg.CollectionTimeout != 90*time.Second || cfg.HTTPTimeout != 12*time.Second || cfg.MaintenanceInterval != 2*time.Hour {
		t.Fatalf("operational overrides not applied: %+v", cfg)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "listen address", key: "RELAYPULSE_LISTEN_ADDR", value: "public.example:8080"},
		{name: "listen port text", key: "RELAYPULSE_LISTEN_ADDR", value: "127.0.0.1:abc"},
		{name: "listen port range", key: "RELAYPULSE_LISTEN_ADDR", value: "127.0.0.1:65536"},
		{name: "data directory", key: "RELAYPULSE_DATA_DIR", value: "."},
		{name: "log level", key: "RELAYPULSE_LOG_LEVEL", value: "verbose"},
		{name: "shutdown timeout", key: "RELAYPULSE_SHUTDOWN_TIMEOUT", value: "0s"},
		{name: "HTTP concurrency", key: "RELAYPULSE_HTTP_CONCURRENCY", value: "0"},
		{name: "collection timeout", key: "RELAYPULSE_COLLECTION_TIMEOUT", value: "500ms"},
		{name: "HTTP timeout", key: "RELAYPULSE_HTTP_TIMEOUT", value: "0s"},
		{name: "maintenance interval", key: "RELAYPULSE_MAINTENANCE_INTERVAL", value: "30s"},
		{name: "public URL scheme", key: "RELAYPULSE_PUBLIC_URL", value: "ftp://example.com"},
		{name: "public URL credentials", key: "RELAYPULSE_PUBLIC_URL", value: "https://user@example.com"},
		{name: "public URL query", key: "RELAYPULSE_PUBLIC_URL", value: "https://example.com/?token=x"},
		{name: "public URL path", key: "RELAYPULSE_PUBLIC_URL", value: "https://example.com/status"},
		{name: "OAuth pair", key: "RELAYPULSE_OAUTH_CLIENT_ID", value: "client"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(func(key string) (string, bool) {
				if key == test.key {
					return test.value, true
				}
				if test.name == "OAuth pair" && key == "RELAYPULSE_OAUTH_CLIENT_SECRET" {
					return "", false
				}
				return "", false
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
