package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr          = "127.0.0.1:8080"
	defaultDataDir             = "data"
	defaultShutdownPeriod      = 10 * time.Second
	defaultHTTPConcurrency     = 3
	defaultCollectionTimeout   = 3 * time.Minute
	defaultHTTPTimeout         = 20 * time.Second
	defaultMaintenanceInterval = 30 * time.Minute
)

type Config struct {
	ListenAddr           string
	DataDir              string
	LogLevel             slog.Level
	ShutdownTimeout      time.Duration
	HTTPConcurrency      int
	CollectionTimeout    time.Duration
	HTTPTimeout          time.Duration
	MaintenanceInterval  time.Duration
	FlareSolverrEndpoint string
	SessionEncryptionKey string
	PublicURL            string
	OAuthClientID        string
	OAuthClientSecret    string
}

func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		ListenAddr:           valueOrDefault(lookup, "RELAYPULSE_LISTEN_ADDR", defaultListenAddr),
		DataDir:              valueOrDefault(lookup, "RELAYPULSE_DATA_DIR", defaultDataDir),
		ShutdownTimeout:      defaultShutdownPeriod,
		HTTPConcurrency:      defaultHTTPConcurrency,
		CollectionTimeout:    defaultCollectionTimeout,
		HTTPTimeout:          defaultHTTPTimeout,
		MaintenanceInterval:  defaultMaintenanceInterval,
		FlareSolverrEndpoint: strings.TrimSpace(valueOrDefault(lookup, "RELAYPULSE_FLARESOLVERR_ENDPOINT", "")),
		SessionEncryptionKey: strings.TrimSpace(valueOrDefault(lookup, "RELAYPULSE_SESSION_ENCRYPTION_KEY", "")),
		PublicURL:            strings.TrimRight(strings.TrimSpace(valueOrDefault(lookup, "RELAYPULSE_PUBLIC_URL", "")), "/"),
		OAuthClientID:        strings.TrimSpace(valueOrDefault(lookup, "RELAYPULSE_OAUTH_CLIENT_ID", "")),
		OAuthClientSecret:    strings.TrimSpace(valueOrDefault(lookup, "RELAYPULSE_OAUTH_CLIENT_SECRET", "")),
	}

	if err := validateListenAddr(cfg.ListenAddr); err != nil {
		return Config{}, fmt.Errorf("RELAYPULSE_LISTEN_ADDR: %w", err)
	}
	if err := validatePublicURL(cfg.PublicURL); err != nil {
		return Config{}, fmt.Errorf("RELAYPULSE_PUBLIC_URL: %w", err)
	}

	cleanDataDir := filepath.Clean(strings.TrimSpace(cfg.DataDir))
	if cleanDataDir == "." || cleanDataDir == "" {
		return Config{}, errors.New("RELAYPULSE_DATA_DIR must name a dedicated directory")
	}
	cfg.DataDir = cleanDataDir

	levelText := valueOrDefault(lookup, "RELAYPULSE_LOG_LEVEL", "info")
	if err := cfg.LogLevel.UnmarshalText([]byte(strings.ToLower(levelText))); err != nil {
		return Config{}, fmt.Errorf("RELAYPULSE_LOG_LEVEL: %w", err)
	}

	if raw, ok := lookup("RELAYPULSE_SHUTDOWN_TIMEOUT"); ok && strings.TrimSpace(raw) != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			return Config{}, errors.New("RELAYPULSE_SHUTDOWN_TIMEOUT must be a positive duration")
		}
		cfg.ShutdownTimeout = duration
	}
	if raw, ok := lookup("RELAYPULSE_HTTP_CONCURRENCY"); ok && strings.TrimSpace(raw) != "" {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || value < 1 || value > 32 {
			return Config{}, errors.New("RELAYPULSE_HTTP_CONCURRENCY must be between 1 and 32")
		}
		cfg.HTTPConcurrency = value
	}
	for key, spec := range map[string]struct {
		target  *time.Duration
		minimum time.Duration
	}{
		"RELAYPULSE_COLLECTION_TIMEOUT":   {&cfg.CollectionTimeout, time.Second},
		"RELAYPULSE_HTTP_TIMEOUT":         {&cfg.HTTPTimeout, time.Second},
		"RELAYPULSE_MAINTENANCE_INTERVAL": {&cfg.MaintenanceInterval, time.Minute},
	} {
		if raw, ok := lookup(key); ok && strings.TrimSpace(raw) != "" {
			duration, err := time.ParseDuration(strings.TrimSpace(raw))
			if err != nil || duration < spec.minimum {
				return Config{}, fmt.Errorf("%s must be at least %s", key, spec.minimum)
			}
			*spec.target = duration
		}
	}

	return cfg, nil
}

func validatePublicURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must not contain credentials, query parameters, or a fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return errors.New("must not contain a path")
	}
	return nil
}

func valueOrDefault(lookup func(string) (string, bool), key, fallback string) string {
	if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func validateListenAddr(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("must be in host:port form")
	}
	if port == "" {
		return errors.New("port is required")
	}
	if host != "" && net.ParseIP(host) == nil && host != "localhost" {
		return errors.New("host must be an IP address or localhost")
	}
	return nil
}
