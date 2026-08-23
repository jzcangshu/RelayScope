package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultListenAddr     = "127.0.0.1:8080"
	defaultDataDir        = "data"
	defaultShutdownPeriod = 10 * time.Second
)

type Config struct {
	ListenAddr           string
	DataDir              string
	LogLevel             slog.Level
	ShutdownTimeout      time.Duration
	FlareSolverrEndpoint string
	SessionEncryptionKey string
	PublicURL            string
	LinuxDOClientID      string
	LinuxDOClientSecret  string
}

func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		ListenAddr:           valueOrDefault(lookup, "RELAYPULSE_LISTEN_ADDR", defaultListenAddr),
		DataDir:              valueOrDefault(lookup, "RELAYPULSE_DATA_DIR", defaultDataDir),
		ShutdownTimeout:      defaultShutdownPeriod,
		FlareSolverrEndpoint: strings.TrimSpace(valueOrDefault(lookup, "RELAYPULSE_FLARESOLVERR_ENDPOINT", "")),
		SessionEncryptionKey: strings.TrimSpace(valueOrDefault(lookup, "RELAYPULSE_SESSION_ENCRYPTION_KEY", "")),
		PublicURL:            strings.TrimRight(strings.TrimSpace(valueOrDefault(lookup, "RELAYPULSE_PUBLIC_URL", "")), "/"),
		LinuxDOClientID:      strings.TrimSpace(valueOrDefault(lookup, "LINUXDO_CLIENT_ID", "")),
		LinuxDOClientSecret:  strings.TrimSpace(valueOrDefault(lookup, "LINUXDO_CLIENT_SECRET", "")),
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
