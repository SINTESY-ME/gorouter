// Package httpx provides the HTTP transport: chi router, middleware, and
// handlers for both the OpenAI-compatible API (/v1) and the dashboard API
// (/api). Handlers are framework-agnostic (http.HandlerFunc) and call
// application services.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/apikey"
	"github.com/jhon/gorouter/internal/providers"
	"github.com/jhon/gorouter/internal/providers/oauth"
)

// Prober validates a provider connection at save time by probing the
// upstream. When the format is "auto", it detects the best format.
type Prober interface {
	Probe(ctx context.Context, conn *domain.Connection, cfg *domain.ProviderConfig) app.ProbeResult
}

// ModelSyncer syncs the model catalog for a provider connection.
type ModelSyncer interface {
	SyncProvider(ctx context.Context, conn *domain.Connection) error
	SyncAll(ctx context.Context)
}

// Server bundles the services and wires the routes. It is constructed once
// at startup; *http.Server is the caller's responsibility.
type Server struct {
	Router          *app.RouterService
	Models          *app.ModelsService
	Providers       *app.ConnectionService
	ProviderConfigs domain.ProviderConfigRepo
	Combos          *app.ComboService
	Keys            *app.ApiKeyService
	Usage           *app.UsageService
	Health          *app.HealthTracker
	Prober          Prober
	ModelSync       ModelSyncer
	ModelRepo       domain.ModelRepo
	Cache           *app.CacheService
	Settings        domain.SettingRepo
	Savings         *app.SavingsTracker
	// RTKCompressorFactory creates a fresh RequestCompressor when the user
	// toggles RTK on via the dashboard. Injected at composition root.
	RTKCompressorFactory func() domain.RequestCompressor
	// CacheFactory creates a fresh ResponseCache when the user toggles the
	// response cache on via the dashboard. Injected at composition root.
	CacheFactory func() domain.ResponseCache
	// HookFactory builds the hook pipeline from enabled hook names when the
	// user toggles hooks via the dashboard. Injected at composition root;
	// a nil pipeline disables hooks (zero hot-path cost).
	HookFactory   func(names []string) (*app.HookPipeline, error)
	SemanticCache *app.SemanticCacheService

	RequireKey  bool
	RateLimiter *app.RateLimiter
	Auth        *app.AuthService
	// KeySecret is the HMAC secret used to stamp/verify the CRC segment of
	// client API keys (sk-{id}-{crc}). When non-empty, malformed keys are
	// rejected by the cheap in-memory check before any repo lookup.
	KeySecret string
	Catalog   *providers.Service
	OAuth     *oauth.Manager
	// BudgetChecker enforces per-key spending caps. Nil disables budget
	// enforcement (all requests pass through regardless of spend).
	BudgetChecker *app.BudgetService
}

// Routes builds the chi router with all endpoints.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(zapLogger)
	r.Use(chimw.Recoverer)
	r.Use(corsMiddleware)

	// Prometheus metrics: public scrape endpoint, standard for monitoring.
	r.Get("/metrics", s.handleMetrics)
	// Health/readiness probes for orchestrators (K8s, Swarm, LB).
	r.Get("/health", s.handleHealth)
	r.Get("/health/liveliness", s.handleLiveliness)
	r.Get("/health/readiness", s.handleReadiness)

	r.Route("/v1", func(r chi.Router) {
		if s.RequireKey {
			r.Use(s.requireApiKey)
		}
		r.Get("/models", s.handleListModels)
		r.Post("/chat/completions", s.handleChatWithFormat(domain.FormatOpenAI))
		r.Post("/completions", s.handleChatWithFormat(domain.FormatOpenAI))  // alias
		r.Post("/messages", s.handleChatWithFormat(domain.FormatAnthropic))  // anthropic-style
		r.Post("/responses", s.handleChatWithFormat(domain.FormatResponses)) // openai responses
		r.Post("/embeddings", s.handlePassthrough("embeddings"))
		r.Post("/images/generations", s.handlePassthrough("images/generations"))
		r.Post("/audio/speech", s.handlePassthrough("audio/speech"))
		r.Post("/audio/transcriptions", s.handlePassthrough("audio/transcriptions"))
		r.Get("/*", s.handleNotImplemented)
	})

	r.Route("/api", func(r chi.Router) {
		// Auth routes are public (not behind requireDashboardToken) so the
		// SPA can bootstrap: status reports whether a password is set and
		// whether the current bearer token is valid; setup sets the first
		// password; login validates it.
		r.Group(func(r chi.Router) {
			r.Get("/auth/status", s.handleAuthStatus)
			r.Post("/auth/setup", s.handleAuthSetup)
			r.Post("/auth/login", s.handleAuthLogin)
			// OAuth browser callback must be public (IdP redirect).
			r.Get("/oauth/{provider}/callback", s.handleOAuthCallback)
		})

		// Dashboard API auth: requireDashboardToken is always mounted but
		// is a no-op when no password is configured (env token or DB hash).
		// This lets the setup flow be unprotected while password-protecting
		// all other /api/* routes once configured.
		r.Group(func(r chi.Router) {
			r.Use(s.requireDashboardToken)
			// dashboard API does not require the OpenAI-style client key; in v1
			// of this router we trust localhost. Add dashboard auth as required.
			r.Get("/providers", s.handleListProviders)
			r.Post("/providers", s.handleCreateProvider)
			r.Put("/providers/{id}", s.handleUpdateProvider)
			r.Delete("/providers/{id}", s.handleDeleteProvider)

			r.Get("/connections", s.handleListConnections)
			r.Post("/connections", s.handleCreateConnection)
			r.Put("/connections/{id}", s.handleUpdateConnection)
			r.Delete("/connections/{id}", s.handleDeleteConnection)
			r.Post("/connections/reorder", s.handleReorderConnections)
			r.Get("/providers/{id}/models", s.handleProviderModels)
			r.Post("/providers/{id}/models", s.handleAddModel)
			r.Post("/providers/{id}/models/sync", s.handleSyncProviderModels)

			r.Get("/provider-catalog", s.handleListCatalog)
			r.Get("/provider-catalog/{id}", s.handleGetCatalog)
			r.Get("/provider-store", s.handleListStore)
			r.Post("/provider-store/install/{id}", s.handleInstallStore)
			r.Delete("/provider-store/{id}", s.handleRemoveStore)

			r.Get("/oauth/providers", s.handleOAuthProviders)
			r.Post("/oauth/{provider}/start", s.handleOAuthStart)
			r.Post("/oauth/{provider}/complete", s.handleOAuthComplete)
			// Callback is public (browser redirect) — registered outside auth group below

			r.Get("/models", s.handleListModelsDashboard)
			r.Get("/models/stats", s.handleModelStats)
			r.Get("/models/all", s.handleListAllModels)
			r.Put("/models/*", s.handleUpdateModel)
			r.Delete("/models/*", s.handleDeleteModel)
			r.Post("/model-pricing", s.handleUpdateModelPricing)

			r.Get("/combos", s.handleListCombos)
			r.Post("/combos", s.handleCreateCombo)
			r.Put("/combos/{id}", s.handleUpdateCombo)
			r.Delete("/combos/{id}", s.handleDeleteCombo)

			r.Get("/keys", s.handleListKeys)
			r.Post("/keys", s.handleCreateKey)
			r.Put("/keys/{id}", s.handleUpdateKey)
			r.Delete("/keys/{id}", s.handleDeleteKey)

			r.Get("/usage/stats", s.handleUsageStats)
			r.Get("/usage/history", s.handleUsageHistory)
			r.Get("/usage/filters", s.handleUsageFilters)
			r.Get("/status", s.handleStatus)

			r.Get("/cache/stats", s.handleCacheStats)
			r.Post("/cache/flush", s.handleCacheFlush)
			r.Get("/semantic-cache/stats", s.handleSemanticCacheStats)
			r.Post("/semantic-cache/flush", s.handleSemanticCacheFlush)
			r.Get("/savings", s.handleSavings)
			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handleUpdateSettings)
		})
	})

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// dashboard UI is served from embedded assets in internal/web. If assets
	// are not embedded (dev mode), fall through to a 404.
	r.Get("/*", staticHandler)
	return r
}

// requireApiKey validates the client's API key against the ApiKeyRepo via
// the ApiKeyService. Both Authorization: Bearer and x-api-key are accepted.
// When the key carries rate limits, the in-memory sliding-window limiter is
// enforced; when it carries budget limits, the spend cap is checked. A key
// with no limits is unlimited. Requests over a limit get 429.
func (s *Server) requireApiKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := bearerToken(r)
		// Priority: a valid dashboard token always wins over an arbitrary
		// "Bearer X" header. This lets the Playground (and dashboard-
		// triggered tests on localhost) hit /v1 without first creating an
		// API key. A real API key (sk-...) is never a dashboard token.
		if bearer != "" && s.Auth != nil {
			if ok, _ := s.Auth.ValidateToken(r.Context(), bearer); ok {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiKeyCtxKey{}, dashboardInternalKey)))
				return
			}
		}
		key := extractApiKey(r)
		if key == "" {
			writeError(w, http.StatusUnauthorized, "missing api key")
			return
		}
		// Fast-path: reject malformed/fabricated keys via the HMAC CRC check
		// before spending a (cached) repo lookup. Not a security boundary —
		// a valid CRC still requires the revocation check below.
		if s.KeySecret != "" && !apikey.Verify(s.KeySecret, key) {
			writeError(w, http.StatusUnauthorized, "invalid api key")
			return
		}
		apiKey, err := s.Keys.Repo.GetByKey(r.Context(), key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "api key check failed")
			return
		}
		if apiKey == nil || !apiKey.IsActive {
			writeError(w, http.StatusUnauthorized, "invalid or revoked api key")
			return
		}
		if len(apiKey.Limits) == 0 && len(apiKey.AllowedModels) == 0 {
			next.ServeHTTP(w, r.WithContext(withApiKey(r.Context(), key, apiKey)))
			return
		}
		// Rate limits: all must pass (AND).
		var rateLimits []domain.KeyLimit
		for _, l := range apiKey.Limits {
			if l.Kind == domain.KeyLimitRate {
				rateLimits = append(rateLimits, l)
			}
		}
		if s.RateLimiter != nil && len(rateLimits) > 0 {
			allowed, retryAfter := s.RateLimiter.Allow(key, rateLimits)
			if !allowed {
				if retryAfter <= 0 {
					retryAfter = 60 * time.Second
				}
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
		}
		// Budget limits: blocked if any is exceeded.
		if s.BudgetChecker != nil {
			for _, l := range apiKey.Limits {
				if l.Kind != domain.KeyLimitBudget || l.Max <= 0 {
					continue
				}
				dur, err := domain.ParseWindowDuration(l.Duration)
				if err != nil || dur <= 0 {
					continue
				}
				result := s.BudgetChecker.Check(r.Context(), key, l.Max, dur)
				if !result.Allowed {
					w.Header().Set("Retry-After", "60")
					writeError(w, http.StatusTooManyRequests, fmt.Sprintf("budget limit exceeded: $%.2f of $%.2f per %s", result.Spent, result.Limit, l.Duration))
					return
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(withApiKey(r.Context(), key, apiKey)))
	})
}

// withApiKey stores the authenticated key string and, when present, the
// key's allowed-models restriction in the context.
func withApiKey(ctx context.Context, key string, apiKey *domain.ApiKey) context.Context {
	ctx = context.WithValue(ctx, apiKeyCtxKey{}, key)
	if len(apiKey.AllowedModels) > 0 {
		ctx = app.WithAllowedModels(ctx, apiKey.AllowedModels)
	}
	return ctx
}

// dashboardInternalKey is a sentinel value stored in apiKeyCtxKey when the
// request was authenticated via the dashboard token instead of an API key.
// Used to distinguish "no key" from "dashboard-authenticated".
const dashboardInternalKey = "__dashboard__"

type apiKeyCtxKey struct{}

// requireDashboardToken validates the dashboard bearer token. Accepts
// either Authorization: Bearer <token> or ?dashboard_token=<token> (for
// browser sessions that can't set headers). When no password is
// configured (env token empty AND no DB hash), auth is disabled and all
// requests pass through (trust localhost). Returns 401 on mismatch.
func (s *Server) requireDashboardToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configured, _ := s.Auth.IsConfigured(r.Context())
		if !configured {
			next.ServeHTTP(w, r)
			return
		}
		token := bearerToken(r)
		ok, _ := s.Auth.ValidateToken(r.Context(), token)
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid or missing dashboard token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractApiKey(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if v := r.Header.Get("x-api-key"); v != "" {
		return v
	}
	// ?api_key= is supported for clients that can't set headers (curl tests).
	if v := r.URL.Query().Get("api_key"); v != "" {
		return v
	}
	return ""
}

func (s *Server) clientApiKey(r *http.Request) string {
	if v, ok := r.Context().Value(apiKeyCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// writeJSON writes a JSON body with status. Failures here are unrecoverable
// (writer broken); we ignore them.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "gorouter_error",
			"code":    status,
		},
	})
}

// statusForError maps domain errors to HTTP status codes.
func statusForError(err error) int {
	// Hooks reject with an explicit status (HookRejectError); it wins over
	// the sentinel matching below.
	var hre *domain.HookRejectError
	if errors.As(err, &hre) && hre.Status != 0 {
		return hre.Status
	}
	// An upstream failure that exhausted routing keeps its real status so the
	// client sees e.g. 429 or 500 instead of a generic gateway error.
	var ue *domain.UpstreamError
	if errors.As(err, &ue) && ue.Status != 0 {
		return ue.Status
	}
	switch {
	case err == nil:
		return http.StatusOK
	case isDomain(err, domain.ErrNotFound):
		return http.StatusNotFound
	case isDomain(err, domain.ErrAlreadyExists):
		return http.StatusConflict
	case isDomain(err, domain.ErrValidation):
		return http.StatusBadRequest
	case isDomain(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized
	case isDomain(err, domain.ErrForbidden):
		return http.StatusForbidden
	case isDomain(err, domain.ErrNoConnection):
		return http.StatusServiceUnavailable
	case isDomain(err, domain.ErrAllModelsFailed):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func isDomain(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), target.Error())
}
