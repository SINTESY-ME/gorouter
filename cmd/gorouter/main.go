// Command gorouter is the composition root: it wires infrastructure adapters
// into application services, builds the HTTP server, and runs it.
//
// One goroutine per long-lived concern; clean shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/config"
	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/db"
	"github.com/jhon/gorouter/internal/infra/executor"
	"github.com/jhon/gorouter/internal/infra/responsecache"
	"github.com/jhon/gorouter/internal/infra/rtk"
	"github.com/jhon/gorouter/internal/infra/translator"
	httpx "github.com/jhon/gorouter/internal/interfaces/http"
	"github.com/jhon/gorouter/internal/providers"
	"github.com/jhon/gorouter/internal/providers/executors"
	"github.com/jhon/gorouter/internal/providers/oauth"
	"github.com/jhon/gorouter/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("gorouter exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	slog.Info("starting gorouter", "home", cfg.HomeDir, "db_driver", cfg.DBDriver, "port", cfg.Port)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	gdb, err := db.Open(ctx, cfg.DBDriver, pickDSN(cfg))
	if err != nil {
		return err
	}
	defer db.Close(gdb)

	// Repos
	connRepo := db.NewConnectionRepo(gdb)
	comboRepo := db.NewComboRepo(gdb)
	keyRepo := db.NewApiKeyRepo(gdb)
	usageRepo := db.NewUsageRepo(gdb)
	modelRepo := db.NewModelRepo(gdb)
	settingRepo := db.NewSettingRepo(gdb)

	// Hot-path caches: wrap repos with short-TTL in-memory caches so the
	// /v1/* request path doesn't hit the database for key validation or
	// connection lookup on every request. Dashboard writes invalidate.
	const cacheTTL = 30 * time.Second
	cachedConns := app.NewConnCache(connRepo, cacheTTL)
	cachedKeys := app.NewApiKeyCache(keyRepo, cacheTTL)
	asyncUsage := app.NewAsyncUsageRecorder(usageRepo)
	defer asyncUsage.Close()

	// Infrastructure adapters
	httpExec := executor.NewHTTPExecutor(time.Duration(cfg.UpstreamTimeoutSeconds) * time.Second)
	exec := &executors.Multi{Default: httpExec}
	tr := translator.New()
	fetcher := app.NewHTTPModelFetcher()
	prober := app.NewProviderProbe()
	registry := app.NewModelRegistry()

	// OAuth (Codex + Gemini CLI); more providers register the same way.
	oauthMgr := oauth.NewManager()
	oauthMgr.Register(&oauth.Codex{})
	oauthMgr.Register(&oauth.GeminiCLI{})
	tokenRefresher := &oauth.Refresher{Manager: oauthMgr, Repo: cachedConns}

	// Application services
	auth := &app.AuthService{EnvToken: cfg.DashboardToken, Repo: settingRepo}
	apiKeys := &app.ApiKeyService{Repo: cachedKeys, Secret: cfg.KeySecret}
	router := app.NewRouterService(comboRepo, cachedConns, exec, tr, asyncUsage)
	router.Tokens = tokenRefresher
	router.Models = modelRepo
	router.Registry = registry
	savings := app.NewSavingsTracker()
	router.Savings = savings

	// Response cache (direct-hash). Disabled when GOROUTER_CACHE_ENABLED=false.
	// Can be toggled live via dashboard settings (persists to SettingRepo).
	var cacheSvc *app.CacheService
	cacheFactory := func() domain.ResponseCache {
		return responsecache.NewMemory(cfg.CacheMaxEntries, cfg.CacheTTL, cfg.CacheSweepInterval)
	}
	if cfg.CacheEnabled {
		mc := cacheFactory().(interface {
			domain.ResponseCache
			Close()
		})
		defer mc.Close()
		cacheSvc = app.NewCacheService(mc)
		router.Cache = cacheSvc
		slog.Info("response cache enabled", "ttl", cfg.CacheTTL, "max_entries", cfg.CacheMaxEntries)
	}
	// Persist initial cache state if not already set.
	if _, err := settingRepo.Get(ctx, "cache_enabled"); err != nil {
		_ = settingRepo.Set(ctx, "cache_enabled", strconv.FormatBool(cfg.CacheEnabled))
	}

	// RTK request token compression. Disabled when GOROUTER_RTK_ENABLED=false.
	// Can be toggled live via dashboard settings (persists to SettingRepo).
	rtkFactory := func() domain.RequestCompressor { return rtk.NewCompressor() }
	if cfg.RTKEnabled {
		router.Compressor = rtkFactory()
		slog.Info("rtk compression enabled")
	}
	// Persist initial RTK state if not already set.
	if _, err := settingRepo.Get(ctx, "rtk_enabled"); err != nil {
		_ = settingRepo.Set(ctx, "rtk_enabled", strconv.FormatBool(cfg.RTKEnabled))
	}

	models := &app.ModelsService{Combos: comboRepo, Connections: cachedConns, Fetcher: fetcher, Models: modelRepo}
	connSvc := &app.ConnectionService{Repo: cachedConns}
	combos := &app.ComboService{Repo: comboRepo, Models: modelRepo}
	usage := &app.UsageService{Repo: usageRepo}
	modelSync := &app.ModelSyncService{
		Connections: cachedConns,
		Models:      modelRepo,
		Fetcher:     fetcher,
		Registry:    registry,
	}

	// Provider catalog + store (YAML presets; install from origin repo)
	providersDir := filepath.Join(cfg.HomeDir, "providers")
	catalog, err := providers.NewCatalog(providersDir)
	if err != nil {
		return err
	}
	catalogSvc := providers.NewService(
		catalog,
		providers.NewStore(providersDir),
		providers.NewGitHubSource("SINTESY-ME", "gorouter"),
	)

	httpx.SetStaticHandler(web.Handler)
	srv := &httpx.Server{
		Router:      router,
		Models:      models,
		Providers:   connSvc,
		Combos:      combos,
		Keys:        apiKeys,
		Usage:       usage,
		Fetcher:     fetcher,
		Prober:      prober,
		ModelSync:   modelSync,
		ModelRepo:   modelRepo,
		Cache:       cacheSvc,
		Settings:    settingRepo,
		Savings:     savings,
		RTKCompressorFactory: rtkFactory,
		CacheFactory: cacheFactory,
		RequireKey:  cfg.RequireKey,
		Auth:        auth,
		RateLimiter: app.NewRateLimiter(),
		Catalog:     catalogSvc,
		OAuth:       oauthMgr,
	}
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Background model catalog sync: runs once on startup (after a brief
	// delay so the server binds first), then every 2 hours.
	go func() {
		time.Sleep(2 * time.Second)
		modelSync.SyncAll(ctx)
		ticker := time.NewTicker(2 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				modelSync.SyncAll(ctx)
			}
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
	return nil
}

// pickDSN returns the DSN for the configured driver: the SQLite file path
// for sqlite, or the GOROUTER_DB_DSN connection string for postgres.
func pickDSN(cfg *config.Config) string {
	if cfg.DBDriver == "postgres" {
		return cfg.DBDSN
	}
	return cfg.DBPath
}