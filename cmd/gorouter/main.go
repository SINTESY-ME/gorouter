// Command gorouter is the composition root: it wires infrastructure adapters
// into application services, builds the HTTP server, and runs it.
//
// One goroutine per long-lived concern; clean shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"encoding/json"
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
	"github.com/jhon/gorouter/internal/infra/metrics"
	"github.com/jhon/gorouter/internal/infra/redis"
	"github.com/jhon/gorouter/internal/infra/responsecache"
	"github.com/jhon/gorouter/internal/infra/rtk"
	"github.com/jhon/gorouter/internal/infra/semanticcache"
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
	providerConfigRepo := db.NewProviderConfigRepo(gdb)
	comboRepo := db.NewComboRepo(gdb)
	keyRepo := db.NewApiKeyRepo(gdb)
	usageRepo := db.NewUsageRepo(gdb)
	modelRepo := db.NewModelRepo(gdb)
	settingRepo := db.NewSettingRepo(gdb)

	// Multi-instance: when GOROUTER_REDIS_URL is set, enable the shared
	// response cache and shared health probes. A configured-but-unreachable
	// Redis degrades gracefully to single-instance behavior with a warning
	// (fail-open, like every other optional subsystem).
	var redisClient *redis.Client
	if cfg.RedisURL != "" {
		rc, rerr := redis.New(cfg.RedisURL)
		if rerr != nil {
			return rerr
		}
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
		perr := rc.Ping(pingCtx)
		pingCancel()
		if perr != nil {
			slog.Warn("redis unreachable; running single-instance", "addr", rc.Addr(), "err", perr)
		} else {
			redisClient = rc
			slog.Info("redis enabled (multi-instance)", "addr", rc.Addr())
		}
	}
	defer func() {
		if redisClient != nil {
			redisClient.Close()
		}
	}()

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
	oauthMgr.Register(&oauth.Antigravity{})
	tokenRefresher := &oauth.Refresher{Manager: oauthMgr, Repo: cachedConns}

	// Application services
	auth := &app.AuthService{EnvToken: cfg.DashboardToken, Repo: settingRepo}
	apiKeys := &app.ApiKeyService{Repo: cachedKeys, Secret: cfg.KeySecret}
	router := app.NewRouterService(comboRepo, cachedConns, exec, tr, asyncUsage)
	router.Tokens = tokenRefresher
	router.Models = modelRepo
	router.Pricing = app.NewPricingCache(modelRepo)
	router.Selector = app.NewConnectionSelector(providerConfigRepo, nil)
	router.Prober = app.NewHealthProber(router.Health, cachedConns, exec, tr, router.Selector)
	if redisClient != nil {
		router.Prober.Shared = redis.NewSharedProbe(redisClient)
	}
	router.TPS = app.NewTPSCache(asyncUsage, 1*time.Minute)
	router.TPSProber = app.NewTPSProber(router.TPS, router)
	savings := app.NewSavingsTracker()
	router.Savings = savings
	budgetSvc := app.NewBudgetService(asyncUsage)

	// Response cache (direct-hash). Controlled by the dashboard settings
	// (persisted in SettingRepo). On boot we read the DB setting; if absent
	// we fall back to the env var default and persist it. The dashboard can
	// toggle it live without a restart.
	var cacheSvc *app.CacheService
	cacheFactory := func() domain.ResponseCache {
		mem := responsecache.NewMemory(cfg.CacheMaxEntries, cfg.CacheTTL, cfg.CacheSweepInterval)
		if redisClient != nil {
			return redis.NewDualCache(mem, redisClient, cfg.CacheTTL)
		}
		return mem
	}
	cacheEnabled := cfg.CacheEnabled
	if v, err := settingRepo.Get(ctx, "cache_enabled"); err == nil {
		cacheEnabled = v == "true"
	} else {
		_ = settingRepo.Set(ctx, "cache_enabled", strconv.FormatBool(cfg.CacheEnabled))
	}
	if cacheEnabled {
		mc := cacheFactory()
		defer mc.Close()
		cacheSvc = app.NewCacheService(mc)
		router.Cache = cacheSvc
		// Caching groups: models listed in the same group share cache entries
		// (operator responsibility — group members must be interchangeable).
		if v, err := settingRepo.Get(ctx, "caching_groups"); err == nil && v != "" {
			var raw map[string][]string
			if jerr := json.Unmarshal([]byte(v), &raw); jerr == nil && len(raw) > 0 {
				groups := map[string]string{}
				for name, models := range raw {
					for _, m := range models {
						groups[m] = name
					}
				}
				cacheSvc.SetCachingGroups(groups)
				slog.Info("caching groups enabled", "groups", len(groups))
			}
		}
		router.MaxCacheHistory = cfg.CacheMaxHistory
		slog.Info("response cache enabled", "ttl", cfg.CacheTTL, "max_entries", cfg.CacheMaxEntries, "max_history", cfg.CacheMaxHistory)
	}

	// RTK request token compression. Same pattern as cache: read from DB
	// setting on boot, fall back to env var, persist initial if absent.
	rtkFactory := func() domain.RequestCompressor { return rtk.NewCompressor() }
	rtkEnabled := cfg.RTKEnabled
	if v, err := settingRepo.Get(ctx, "rtk_enabled"); err == nil {
		rtkEnabled = v == "true"
	} else {
		_ = settingRepo.Set(ctx, "rtk_enabled", strconv.FormatBool(cfg.RTKEnabled))
	}
	if rtkEnabled {
		router.Compressor = rtkFactory()
		slog.Info("rtk compression enabled")
	}

	// Semantic cache (vector-similarity). Same pattern: read the DB
	// settings on boot, fall back to env defaults, persist if absent.
	// Requires an embedding model to be configured; without one the
	// feature stays disabled (even if the toggle says on).
	var semanticSvc *app.SemanticCacheService
	semanticModel := cfg.SemanticCacheModel
	if v, err := settingRepo.Get(ctx, "semantic_cache_model"); err == nil && v != "" {
		semanticModel = v
	} else if semanticModel != "" {
		_ = settingRepo.Set(ctx, "semantic_cache_model", semanticModel)
	}
	semanticMode := cfg.SemanticCacheMode
	if v, err := settingRepo.Get(ctx, "semantic_cache_mode"); err == nil {
		semanticMode = v
	} else {
		_ = settingRepo.Set(ctx, "semantic_cache_mode", semanticMode)
	}
	semanticEnabled := cfg.SemanticCacheEnabled
	if v, err := settingRepo.Get(ctx, "semantic_cache_enabled"); err == nil {
		semanticEnabled = v == "true"
	} else {
		_ = settingRepo.Set(ctx, "semantic_cache_enabled", strconv.FormatBool(cfg.SemanticCacheEnabled))
	}
	if semanticModel != "" {
		embedder := semanticcache.NewGorouterEmbeddingProvider("http://127.0.0.1:"+cfg.Port, "", semanticModel)
		semanticFactory := func() domain.SemanticCache {
			return semanticcache.NewMemory(1000, 10*time.Minute, time.Minute)
		}
		memCache := semanticFactory()
		defer memCache.Close()
		semanticSvc = app.NewSemanticCacheService(memCache, embedder, cfg.SemanticCacheThreshold, semanticMode)
		semanticSvc.SetEnabled(semanticEnabled)
		router.SemanticCache = semanticSvc
		if semanticEnabled {
			slog.Info("semantic cache enabled", "model", semanticModel, "mode", semanticMode, "threshold", cfg.SemanticCacheThreshold)
		}
	} else if semanticEnabled {
		slog.Warn("semantic cache requested but no embedding model configured; disabled")
	}

	models := &app.ModelsService{Combos: comboRepo, Models: modelRepo}
	connSvc := &app.ConnectionService{Repo: cachedConns}
	combos := &app.ComboService{Repo: comboRepo, Models: modelRepo}
	usage := &app.UsageService{Repo: usageRepo}
	modelSync := &app.ModelSyncService{
		Connections: cachedConns,
		Configs:     providerConfigRepo,
		Models:      modelRepo,
		Fetcher:     fetcher,
		Registry:    registry,
		OnSynced:    router.RefreshPricingCache,
	}

	// Hook pipeline (PreCall/PostCall/PostCallFailure). Controlled by the
	// dashboard settings (persisted in SettingRepo); an empty list leaves
	// Router.Hooks nil so every hook point is skipped at zero cost.
	if v, err := settingRepo.Get(ctx, "hooks_enabled"); err == nil && v != "" {
		var names []string
		if jerr := json.Unmarshal([]byte(v), &names); jerr == nil {
			hp, herr := app.NewHookPipeline(names)
			if herr != nil {
				return herr // fail fast on unknown hook name
			}
			router.Hooks = hp
			if len(names) > 0 {
				slog.Info("hooks enabled", "hooks", names)
			}
		}
	}
	// Webhook URL: a persisted setting overrides the env default; applied to
	// the active webhook_logging hook (and the settings API).
	webhookURL := os.Getenv("GOROUTER_HOOK_WEBHOOK_URL")
	if v, err := settingRepo.Get(ctx, "webhook_url"); err == nil && v != "" {
		webhookURL = v
	}
	app.SetWebhookURL(webhookURL)
	if webhookURL != "" {
		slog.Info("webhook_logging url set", "url", webhookURL)
	}

	// Provider catalog + store (YAML presets; install from origin repo)
	providersDir := filepath.Join(cfg.HomeDir, "providers")
	catalog, err := providers.NewCatalog(providersDir)
	if err != nil {
		return err
	}
	router.Selector.Catalog = catalog
	modelSync.Catalog = catalog
	catalogSvc := providers.NewService(
		catalog,
		providers.NewStore(providersDir),
		providers.NewGitHubSource("SINTESY-ME", "gorouter"),
	)

	httpx.SetStaticHandler(web.Handler)

	// Prometheus gauges computed at scrape time from existing in-memory state
	// (health, cache, savings). Request counters are fed by the "prometheus"
	// hook when enabled; these gauges work regardless.
	if router.Health != nil {
		metrics.Default.Gauge("gorouter_health_unhealthy", "Count of unhealthy model/connection triples", func() float64 {
			return float64(router.Health.Summary().Unhealthy)
		})
		metrics.Default.Gauge("gorouter_health_probing", "Count of triples currently being probed", func() float64 {
			return float64(router.Health.Summary().Probing)
		})
		metrics.Default.Gauge("gorouter_health_healthy", "Count of healthy model/connection triples", func() float64 {
			return float64(router.Health.Summary().Healthy)
		})
		metrics.Default.Gauge("gorouter_health_total_keys", "Total tracked (combo, model, connection) triples", func() float64 {
			return float64(router.Health.Summary().TotalKeys)
		})
	}
	metrics.Default.Gauge("gorouter_cache_entries", "Response cache entries (0 when disabled)", func() float64 {
		if cacheSvc == nil {
			return 0
		}
		return float64(cacheSvc.Stats().Entries)
	})
	metrics.Default.Gauge("gorouter_cache_hits", "Response cache hits", func() float64 {
		if cacheSvc == nil {
			return 0
		}
		return float64(cacheSvc.Stats().Hits)
	})
	metrics.Default.Gauge("gorouter_cache_misses", "Response cache misses", func() float64 {
		if cacheSvc == nil {
			return 0
		}
		return float64(cacheSvc.Stats().Misses)
	})
	metrics.Default.Gauge("gorouter_semantic_cache_entries", "Semantic cache entries (0 when disabled)", func() float64 {
		if semanticSvc == nil {
			return 0
		}
		return float64(semanticSvc.Stats().Entries)
	})
	metrics.Default.Gauge("gorouter_semantic_cache_hits", "Semantic cache hits", func() float64 {
		if semanticSvc == nil {
			return 0
		}
		return float64(semanticSvc.Stats().Hits)
	})
	metrics.Default.Gauge("gorouter_semantic_cache_misses", "Semantic cache misses", func() float64 {
		if semanticSvc == nil {
			return 0
		}
		return float64(semanticSvc.Stats().Misses)
	})
	metrics.Default.Gauge("gorouter_savings_cache_tokens", "Tokens saved by response cache hits", func() float64 {
		return float64(savings.Stats().CacheTokensSaved)
	})
	metrics.Default.Gauge("gorouter_savings_cache_cost_usd", "USD saved by response cache hits", func() float64 {
		return savings.Stats().CacheCostSaved
	})
	metrics.Default.Gauge("gorouter_savings_rtk_bytes", "Bytes saved by RTK compression", func() float64 {
		return float64(savings.Stats().RTKBytesSaved)
	})
	metrics.Default.Gauge("gorouter_savings_rtk_cost_usd", "USD saved by RTK compression", func() float64 {
		return savings.Stats().RTKCostSaved
	})
	metrics.Default.Gauge("gorouter_savings_semantic_tokens", "Tokens saved by semantic cache hits", func() float64 {
		return float64(savings.Stats().SemanticTokensSaved)
	})
	metrics.Default.Gauge("gorouter_savings_semantic_cost_usd", "USD saved by semantic cache hits", func() float64 {
		return savings.Stats().SemanticCostSaved
	})
	metrics.Default.Gauge("gorouter_uptime_seconds", "Seconds since gorouter started", func() float64 {
		return metrics.Default.Uptime().Seconds()
	})

	srv := &httpx.Server{
		Router:               router,
		Models:               models,
		Providers:            connSvc,
		ProviderConfigs:      providerConfigRepo,
		Combos:               combos,
		Keys:                 apiKeys,
		Usage:                usage,
		Health:               router.Health,
		Prober:               prober,
		ModelSync:            modelSync,
		ModelRepo:            modelRepo,
		Cache:                cacheSvc,
		Settings:             settingRepo,
		Savings:              savings,
		RTKCompressorFactory: rtkFactory,
		CacheFactory:         cacheFactory,
		HookFactory:          app.NewHookPipeline,
		SemanticCache:        semanticSvc,
		RequireKey:           cfg.RequireKey,
		Auth:                 auth,
		KeySecret:            cfg.KeySecret,
		RateLimiter:          app.NewRateLimiter(),
		Catalog:              catalogSvc,
		OAuth:                oauthMgr,
		BudgetChecker:        budgetSvc,
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

	// Pre-load pricing cache from the DB so the first requests have
	// pricing data before the initial sync completes.
	router.RefreshPricingCache(context.Background())
	// Pre-load provider config cache (load-balance strategies).
	router.RefreshProviderCache(context.Background())

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
