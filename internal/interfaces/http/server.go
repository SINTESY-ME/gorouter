// Package httpx provides the HTTP transport: chi router, middleware, and
// handlers for both the OpenAI-compatible API (/v1) and the dashboard API
// (/api). Handlers are framework-agnostic (http.HandlerFunc) and call
// application services.
package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
)

// Fetcher is used by the dashboard to auto-fetch models for a given provider
// connection. Injected at the composition root.
type FetcherProvider interface {
	Fetch(ctx context.Context, c *domain.Connection) ([]domain.ModelInfo, error)
}

// Prober validates a provider connection at save time by probing the
// upstream. When the format is "auto", it detects the best format.
type Prober interface {
	Probe(ctx context.Context, conn *domain.Connection) app.ProbeResult
}

// ModelSyncer syncs the model catalog for a provider connection.
type ModelSyncer interface {
	SyncProvider(ctx context.Context, conn *domain.Connection) error
	SyncAll(ctx context.Context)
}

// Server bundles the services and wires the routes. It is constructed once
// at startup; *http.Server is the caller's responsibility.
type Server struct {
	Router    *app.RouterService
	Models    *app.ModelsService
	Providers *app.ConnectionService
	Combos    *app.ComboService
	Keys      *app.ApiKeyService
	Usage     *app.UsageService
	Fetcher   FetcherProvider
	Prober    Prober
	ModelSync ModelSyncer
	ModelRepo domain.ModelRepo

	RequireKey     bool
	RateLimiter    *app.RateLimiter
	Auth           *app.AuthService
}

// Routes builds the chi router with all endpoints.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(zapLogger)
	r.Use(chimw.Recoverer)
	r.Use(corsMiddleware)

	r.Route("/v1", func(r chi.Router) {
		if s.RequireKey {
			r.Use(s.requireApiKey)
		}
		r.Get("/models", s.handleListModels)
		r.Post("/chat/completions", s.handleChatWithFormat(domain.FormatOpenAI))
		r.Post("/completions", s.handleChatWithFormat(domain.FormatOpenAI)) // alias
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
		r.Post("/providers/reorder", s.handleReorderProviders)
		r.Get("/providers/{id}/models", s.handleProviderModels)
		r.Post("/providers/{id}/models", s.handleAddModel)
		r.Post("/providers/{id}/models/sync", s.handleSyncProviderModels)
		r.Get("/models", s.handleListModelsDashboard)
		r.Put("/models/*", s.handleUpdateModel)
		r.Delete("/models/*", s.handleDeleteModel)

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
// When the key has a rate_limit_rpm > 0, the in-memory token bucket is
// enforced; requests over the limit get 429.
func (s *Server) requireApiKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractApiKey(r)
		if key == "" {
			writeError(w, http.StatusUnauthorized, "missing api key")
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
		if s.RateLimiter != nil && !s.RateLimiter.Allow(key, apiKey.RateLimitRPM) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		ctx := context.WithValue(r.Context(), apiKeyCtxKey{}, key)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

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