package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
	"time"
)

// DTOs intentionally mirror what the dashboard React app posts. We accept
// extra fields; only the documented ones are read.

type createProviderRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	BaseURL     string `json:"base_url"`
	Format      string `json:"format"`
	Auth        string `json:"auth"`
	LoadBalance string `json:"load_balance"`
	TemplateID  string `json:"template_id"` // optional catalog preset
}

type createConnectionRequest struct {
	ProviderID string `json:"provider_id"`
	Name       string `json:"name"`
	APIKey     string `json:"api_key"`
	Priority   int    `json:"priority"`
	IsActive   *bool  `json:"is_active"`
}

// ---- Providers ----

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	if s.ProviderConfigs == nil {
		writeJSON(w, http.StatusOK, []domain.ProviderConfig{})
		return
	}
	ps, err := s.ProviderConfigs.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var req createProviderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.ProviderConfigs == nil {
		writeError(w, http.StatusServiceUnavailable, "provider configs not available")
		return
	}

	var templateAPIKey string
	if req.TemplateID != "" && s.Catalog != nil {
		if def := s.Catalog.Lookup(req.TemplateID); def != nil {
			applyTemplate(def, &req.ID, &req.BaseURL, &req.Format, &req.Auth, &templateAPIKey)
			if req.Name == "" {
				req.Name = def.Display.Name
			}
		}
	}

	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	cfg := &domain.ProviderConfig{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		BaseURL:     app.NormalizeBaseURL(req.BaseURL),
		Format:      domain.Format(req.Format),
		Auth:        domain.AuthScheme(req.Auth),
		LoadBalance: req.LoadBalance,
	}

	if cfg.Format == "" {
		cfg.Format = domain.FormatAuto
	}

	if err := s.ProviderConfigs.Create(r.Context(), cfg); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}

	if s.Router != nil {
		s.Router.RefreshProviderCache(r.Context())
	}

	writeJSON(w, http.StatusCreated, cfg)
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	if s.ProviderConfigs == nil {
		writeError(w, http.StatusServiceUnavailable, "provider configs not available")
		return
	}

	id := chi.URLParam(r, "id")
	existing, err := s.ProviderConfigs.Get(r.Context(), id)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}

	var req createProviderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing.Name = orDefault(req.Name, existing.Name)
	existing.Description = orDefault(req.Description, existing.Description)
	existing.BaseURL = app.NormalizeBaseURL(orDefault(req.BaseURL, existing.BaseURL))
	if req.Format != "" {
		existing.Format = domain.Format(req.Format)
	}
	if req.Auth != "" {
		existing.Auth = domain.AuthScheme(req.Auth)
	}
	if req.LoadBalance == "failover" || req.LoadBalance == "round-robin" {
		existing.LoadBalance = req.LoadBalance
	}

	if err := s.ProviderConfigs.Update(r.Context(), existing); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}

	if s.Router != nil {
		s.Router.RefreshProviderCache(r.Context())
	}
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if s.ProviderConfigs == nil {
		writeError(w, http.StatusServiceUnavailable, "provider configs not available")
		return
	}
	if err := s.ProviderConfigs.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	if s.Router != nil {
		s.Router.RefreshProviderCache(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Connections ----

func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := s.Providers.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range conns {
		if conns[i].APIKey != "" {
			conns[i].APIKey = maskKey(conns[i].APIKey)
		}
	}
	writeJSON(w, http.StatusOK, conns)
}

func (s *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	var req createConnectionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	c := &domain.Connection{
		ProviderID: req.ProviderID,
		Name:       req.Name,
		APIKey:     req.APIKey,
		Priority:   req.Priority,
		IsActive:   req.IsActive == nil || *req.IsActive,
	}

	if s.ProviderConfigs != nil {
		cfg, err := s.ProviderConfigs.GetByProviderID(r.Context(), req.ProviderID)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid provider_id: %v", err))
			return
		}
		if s.Prober != nil {
			result := s.Prober.Probe(r.Context(), c, cfg)
			if result.Error != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("connection validation failed: %v", result.Error))
				return
			}
			if cfg.Format == "" || cfg.Format == domain.FormatAuto {
				cfg.Format = result.Format
				_ = s.ProviderConfigs.Update(r.Context(), cfg)
			}
		}
	}

	if err := s.Providers.Create(r.Context(), c); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleUpdateConnection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.Providers.Repo.Get(r.Context(), id)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}

	var req createConnectionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing.Name = orDefault(req.Name, existing.Name)
	existing.ProviderID = orDefault(req.ProviderID, existing.ProviderID)
	existing.Priority = orDefaultInt(req.Priority, existing.Priority)
	if req.APIKey != "" {
		existing.APIKey = req.APIKey
	}
	if req.IsActive != nil {
		existing.IsActive = *req.IsActive
	}

	if s.ProviderConfigs != nil {
		cfg, err := s.ProviderConfigs.GetByProviderID(r.Context(), existing.ProviderID)
		if err == nil && s.Prober != nil {
			result := s.Prober.Probe(r.Context(), existing, cfg)
			if result.Error != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("connection validation failed: %v", result.Error))
				return
			}
			if cfg.Format == "" || cfg.Format == domain.FormatAuto {
				cfg.Format = result.Format
				_ = s.ProviderConfigs.Update(r.Context(), cfg)
			}
		}
	}

	if err := s.Providers.Update(r.Context(), existing); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	existing.APIKey = maskKey(existing.APIKey)
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := s.Providers.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderConnections(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Order []string `json:"order"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Providers.Reorder(r.Context(), req.Order); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	if s.ModelRepo == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	entries, err := s.ModelRepo.ListByProvider(r.Context(), providerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []domain.ModelEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleSyncProviderModels triggers an on-demand sync of the provider's
// model catalog by fetching /v1/models from the upstream and upserting
// entries into the database.
func (s *Server) handleSyncProviderModels(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	
	// We need a connection to sync from. Find the first active one for this provider.
	conns, err := s.Providers.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var conn *domain.Connection
	for i := range conns {
		if conns[i].ProviderID == providerID && conns[i].IsActive {
			conn = &conns[i]
			break
		}
	}
	if conn == nil {
		writeError(w, http.StatusBadRequest, "no active connection available to sync models")
		return
	}

	if s.ModelSync == nil {
		writeError(w, http.StatusServiceUnavailable, "model sync not configured")
		return
	}
	if err := s.ModelSync.SyncProvider(r.Context(), conn); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	entries, _ := s.ModelRepo.ListByProvider(r.Context(), providerID)
	if entries == nil {
		entries = []domain.ModelEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleAddModel lets the user add a model manually to a provider's catalog.
// This is needed for providers that don't expose /v1/models.
func (s *Server) handleAddModel(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	
	var req struct {
		ModelID string `json:"model_id"`
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Context int    `json:"context"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ModelID == "" {
		writeError(w, http.StatusBadRequest, "model_id is required")
		return
	}
	kind := domain.KindLLM
	if req.Kind != "" {
		kind = domain.ModelKind(req.Kind)
	}
	entry := &domain.ModelEntry{
		ID:         providerID + "/" + req.ModelID,
		ProviderID: providerID,
		ModelID:    req.ModelID,
		Name:       orDefault(req.Name, req.ModelID),
		Kind:       kind,
		Source:     "manual",
		IsActive:   true,
		Context:    req.Context,
	}
	if err := s.ModelRepo.Upsert(r.Context(), entry); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

// handleUpdateModel updates a model entry (activate/deactivate, change Kind).
func (s *Server) handleUpdateModel(w http.ResponseWriter, r *http.Request) {
	// Model IDs contain "/" (e.g. "openadapter/whisper-1"), so we extract
	// everything after "/api/models/" from the path.
	id := strings.TrimPrefix(r.URL.Path, "/api/models/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "model id is required")
		return
	}
	var req struct {
		IsActive *bool  `json:"is_active"`
		Kind     string `json:"kind"`
		Name     string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.ModelRepo.Get(r.Context(), id)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	if req.IsActive != nil {
		existing.IsActive = *req.IsActive
	}
	if req.Kind != "" {
		existing.Kind = domain.ModelKind(req.Kind)
	}
	if req.Name != "" {
		existing.Name = req.Name
	}
	if err := s.ModelRepo.Upsert(r.Context(), existing); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

// handleUpdateModelPricing sets a manual price override for a model. The
// pricing is stored with Source="manual" so subsequent sync runs don't
// overwrite it. The model ID is passed in the request body as "model_id".
func (s *Server) handleUpdateModelPricing(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelID string                `json:"model_id"`
		Pricing domain.ModelPricing   `json:"pricing"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ModelID == "" {
		writeError(w, http.StatusBadRequest, "model_id is required")
		return
	}
	existing, err := s.ModelRepo.Get(r.Context(), req.ModelID)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	req.Pricing.Source = "manual"
	existing.Pricing = req.Pricing
	existing.UpdatedAt = time.Now()
	if err := s.ModelRepo.Upsert(r.Context(), existing); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

// handleDeleteModel removes a model from the catalog (hard delete).
func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/models/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "model id is required")
		return
	}
	if err := s.ModelRepo.Delete(r.Context(), id); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Combos ----

type comboDTO struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	Models   []string `json:"models"`
	Strategy string   `json:"strategy"`
}

func (s *Server) handleListCombos(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Combos.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) handleCreateCombo(w http.ResponseWriter, r *http.Request) {
	var req comboDTO
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := &domain.Combo{
		Name:     req.Name,
		Models:   req.Models,
		Strategy: req.Strategy,
	}
	if err := s.Combos.Create(r.Context(), c); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleUpdateCombo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req comboDTO
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.Combos.Repo.Get(r.Context(), id)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	existing.Name = orDefault(req.Name, existing.Name)
	existing.Strategy = orDefault(req.Strategy, existing.Strategy)
	if len(req.Models) > 0 {
		existing.Models = req.Models
	}
	if err := s.Combos.Update(r.Context(), existing); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteCombo(w http.ResponseWriter, r *http.Request) {
	if err := s.Combos.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Api keys ----

type keyDTO struct {
	Name         string `json:"name"`
	IsActive     *bool  `json:"is_active"`
	RateLimitRPM *int   `json:"rate_limit_rpm"`
}

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	ks, err := s.Keys.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range ks {
		ks[i].Key = maskKey(ks[i].Key)
	}
	writeJSON(w, http.StatusOK, ks)
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var req keyDTO
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rpm := 0
	if req.RateLimitRPM != nil {
		rpm = *req.RateLimitRPM
	}
	k, err := s.Keys.Create(r.Context(), req.Name, rpm)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	// The full key is returned exactly once here; the dashboard must show
	// it and warn the user it won't be shown again.
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) handleUpdateKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := lookupKey(s.Keys, r.Context(), id)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	var req keyDTO
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.IsActive != nil {
		existing.IsActive = *req.IsActive
	}
	if req.RateLimitRPM != nil {
		existing.RateLimitRPM = *req.RateLimitRPM
	}
	if err := s.Keys.Update(r.Context(), existing); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	existing.Key = maskKey(existing.Key)
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	if err := s.Keys.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Usage ----

func (s *Server) handleUsageStats(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}
	stats, err := s.Usage.Stats(r.Context(), period)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleUsageHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	h, err := s.Usage.History(r.Context(), limit)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// ---- Models (dashboard aggregate) ----

// handleListModelsDashboard returns the aggregate model list (combos +
// auto-fetched connection models) in a flat array. The dashboard Models page
// uses this; the OpenAI-style /v1/models endpoint delegates to the same
// ModelsService.List but wraps the response in {object, data}.
func (s *Server) handleListModelsDashboard(w http.ResponseWriter, r *http.Request) {
	models, err := s.Models.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if models == nil {
		models = []domain.ModelInfo{}
	}
	writeJSON(w, http.StatusOK, models)
}

// handleModelStats returns per-model performance stats (avg TPS, latency, requests).
func (s *Server) handleModelStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.Usage.Repo.ModelStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if stats == nil {
		stats = map[string]*domain.ModelStat{}
	}
	writeJSON(w, http.StatusOK, stats)
}

// ---- helpers ----

func decodeJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return errors.New("empty body")
	}
	return json.Unmarshal(body, v)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// maskKey keeps the prefix visible and elides the middle. "sk-abc...xyz"
// is enough for the dashboard to show "is this the key I think" without
// leaking the secret over the wire on every list call.
func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 12 {
		return k[:3] + "..." + k[len(k)-2:]
	}
	return k[:6] + "..." + k[len(k)-4:]
}

// lookupKey finds an api key by id via List. The dataset is small (usually
// tens of keys); a dedicated Get-by-id repo method isn't worth the extra
// surface yet.
func lookupKey(s *app.ApiKeyService, ctx context.Context, id string) (*domain.ApiKey, error) {
	ks, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ks {
		if ks[i].ID == id {
			return &ks[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

// --- ProviderConfig (provider grouping metadata) ---

