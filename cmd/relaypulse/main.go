package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"relaypulse/internal/adapter"
	"relaypulse/internal/admin"
	"relaypulse/internal/bootstrap"
	"relaypulse/internal/challenge"
	"relaypulse/internal/collector"
	"relaypulse/internal/config"
	"relaypulse/internal/httpserver"
	"relaypulse/internal/linuxdo"
	"relaypulse/internal/logging"
	"relaypulse/internal/scheduler"
	"relaypulse/internal/session"
	"relaypulse/internal/store"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger := logging.NewJSON(os.Stdout, cfg.LogLevel)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	dbStore, err := store.Open(context.Background(), filepath.Join(cfg.DataDir, "relaypulse.db"))
	if err != nil {
		return fmt.Errorf("open data store: %w", err)
	}
	defer dbStore.Close()
	adminPassword, err := loadOrCreateAdminPassword(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("prepare administrator password: %w", err)
	}
	auth, err := admin.NewAuth(adminPassword)
	if err != nil {
		return fmt.Errorf("build administrator auth: %w", err)
	}
	if err := bootstrap.EnsureInitialSites(context.Background(), dbStore); err != nil {
		return fmt.Errorf("seed initial sites: %w", err)
	}
	if err := bootstrap.EnsureInitialRules(context.Background(), dbStore); err != nil {
		return fmt.Errorf("seed initial model rules: %w", err)
	}
	registry, err := adapter.NewRegistry(
		adapter.NewAPIAdapter{},
		adapter.NewAPIProbeAdapter(),
		adapter.AIAPIAdapter{},
		adapter.CustomProbeAdapter(),
		adapter.ModelMarketAdapter(),
		adapter.ModelPulseAdapter{},
		adapter.ModelProbeAdapter{},
		adapter.Sub2MonitorAdapter{},
		adapter.UptimeKumaAdapter{},
		adapter.XAPIPoolAdapter{},
	)
	if err != nil {
		return fmt.Errorf("build adapter registry: %w", err)
	}
	var challengeProvider adapter.ChallengeProvider
	if cfg.FlareSolverrEndpoint != "" {
		provider, providerErr := challenge.NewFlareSolverr(cfg.FlareSolverrEndpoint)
		if providerErr != nil {
			return fmt.Errorf("configure FlareSolverr: %w", providerErr)
		}
		challengeProvider = provider
	}
	baseFetcher := adapter.HTTPFetcher{Client: &http.Client{Timeout: 20 * time.Second}, UserAgent: "RelayPulse/0.1", MaxBytes: 2 << 20, Challenge: challengeProvider}
	var siteFetcher adapter.Fetcher = baseFetcher
	var sessionVault *session.Vault
	if cfg.SessionEncryptionKey != "" {
		vault, vaultErr := session.NewVault(cfg.SessionEncryptionKey)
		if vaultErr != nil {
			return fmt.Errorf("configure session vault: %w", vaultErr)
		}
		sessionVault = vault
		siteFetcher = session.Provider{Store: dbStore, Vault: vault, Base: baseFetcher}
	}
	siteCollector, err := collector.New(collector.Options{
		Store: dbStore, Registry: registry, Fetcher: siteFetcher, Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("build collector: %w", err)
	}
	if err := siteCollector.ReloadMatcher(context.Background()); err != nil {
		return fmt.Errorf("refresh model matches: %w", err)
	}
	siteScheduler := scheduler.New(dbStore, siteCollector, logger, time.Now)
	handler, err := httpserver.NewHandler(httpserver.Options{
		Logger:       logger,
		Version:      version,
		Store:        dbStore,
		Auth:         auth,
		Collector:    siteCollector,
		SessionVault: sessionVault,
		PublicURL:    cfg.PublicURL,
		SessionSync:  session.NewSyncManager(time.Now),
		LinuxDO:      linuxdo.New(linuxdo.Config{ClientID: cfg.OAuthClientID, ClientSecret: cfg.OAuthClientSecret, CallbackURL: cfg.PublicURL + "/api/v1/auth/linuxdo/callback"}, dbStore),
	})
	if err != nil {
		return fmt.Errorf("build HTTP handler: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server starting", "address", cfg.ListenAddr, "version", version)
		serverErrors <- server.ListenAndServe()
	}()

	stopContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runMaintenance(stopContext, dbStore, logger)
	siteScheduler.Start(stopContext)

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-stopContext.Done():
		logger.Info("server stopping")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	siteScheduler.Stop()
	logger.Info("server stopped")
	return nil
}

func loadOrCreateAdminPassword(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "admin-password.txt")
	if raw, err := os.ReadFile(path); err == nil && len(raw) >= 16 {
		return string(raw), nil
	}
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	password := base64.RawURLEncoding.EncodeToString(bytes)
	if err := os.WriteFile(path, []byte(password), 0o600); err != nil {
		return "", err
	}
	return password, nil
}

func runMaintenance(ctx context.Context, dbStore *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			removed, err := dbStore.Cleanup(ctx, time.Now().UTC().Add(-72*time.Hour), 500)
			if err != nil {
				logger.Warn("maintenance cleanup failed", "error", err)
				continue
			}
			if removed > 0 {
				logger.Info("maintenance cleanup complete", "rows", removed)
			}
		case <-ctx.Done():
			return
		}
	}
}
