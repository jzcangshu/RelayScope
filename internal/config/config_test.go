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
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"RELAYPULSE_LISTEN_ADDR":      "localhost:9090",
		"RELAYPULSE_DATA_DIR":         "var/state",
		"RELAYPULSE_LOG_LEVEL":        "debug",
		"RELAYPULSE_SHUTDOWN_TIMEOUT": "3s",
		"RELAYPULSE_PUBLIC_URL":       "https://status.example.com/",
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
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "listen address", key: "RELAYPULSE_LISTEN_ADDR", value: "public.example:8080"},
		{name: "data directory", key: "RELAYPULSE_DATA_DIR", value: "."},
		{name: "log level", key: "RELAYPULSE_LOG_LEVEL", value: "verbose"},
		{name: "shutdown timeout", key: "RELAYPULSE_SHUTDOWN_TIMEOUT", value: "0s"},
		{name: "public URL scheme", key: "RELAYPULSE_PUBLIC_URL", value: "ftp://example.com"},
		{name: "public URL credentials", key: "RELAYPULSE_PUBLIC_URL", value: "https://user@example.com"},
		{name: "public URL query", key: "RELAYPULSE_PUBLIC_URL", value: "https://example.com/?token=x"},
		{name: "public URL path", key: "RELAYPULSE_PUBLIC_URL", value: "https://example.com/status"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(func(key string) (string, bool) {
				if key == test.key {
					return test.value, true
				}
				return "", false
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
