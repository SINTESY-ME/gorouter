package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/providers"
)

// ModelSyncService synchronizes the model catalog by fetching /v1/models
// from each active provider connection, enriching the results with data from
// the ModelRegistry (external public APIs), and upserting entries into the
// ModelRepo. Models that disappear from the provider are deactivated (if
// sync-sourced); manual entries are always preserved.
type ModelSyncService struct {
	Connections domain.ConnectionRepo
	Models      domain.ModelRepo
	Configs     domain.ProviderConfigRepo
	Fetcher     domain.ModelFetcher
	Catalog     *providers.Catalog
	Registry    *ModelRegistry
	// OnSynced is called after each provider sync completes (even on
	// partial errors). Used to refresh in-memory caches (e.g. the pricing
	// cache in RouterService). Optional; nil means no callback.
	OnSynced func(ctx context.Context)
}

// SyncAll syncs every active connection. Errors for individual providers are
// logged but don't abort the loop.
func (s *ModelSyncService) SyncAll(ctx context.Context) {
	conns, err := s.Connections.List(ctx)
	if err != nil {
		slog.Error("model sync: list connections failed", "err", err)
		return
	}
	for _, c := range conns {
		if !c.IsActive {
			continue
		}
		if err := s.SyncProvider(ctx, &c); err != nil {
			slog.Warn("model sync: provider failed", "provider", c.ProviderID, "err", err)
		}
	}
}

// SyncProvider fetches models from the provider's /v1/models endpoint,
// resolves each model's Kind (from provider metadata, external registries, or
// name heuristic), and upserts entries in a single batch. Models that were
// sync-sourced and are no longer returned are deactivated.
func (s *ModelSyncService) SyncProvider(ctx context.Context, conn *domain.Connection) error {
	cfg, err := s.Configs.GetByProviderID(ctx, conn.ProviderID)
	if err != nil {
		return err
	}
	fetched, err := s.Fetcher.Fetch(ctx, conn, cfg)
	if err != nil {
		return err
	}
	// Track whether the API itself returned models. Only when the API
	// returned at least one model is it safe to deactivate stale entries.
	// If the API returned nothing (empty 200, transient glitch, parse
	// quirk), deactivating would mass-inactive the entire catalog.
	apiReturnedModels := len(fetched) > 0
	if len(fetched) == 0 {
		if s.Catalog != nil {
			if def := s.Catalog.Lookup(conn.ProviderID); def != nil && len(def.Models) > 0 {
				fetched = make([]domain.ModelInfo, 0, len(def.Models))
				for _, m := range def.Models {
					fetched = append(fetched, domain.ModelInfo{ID: m.ID, Object: "model"})
				}
			}
		}
	}
	if len(fetched) == 0 {
		slog.Warn("model sync: no models returned by provider or catalog preset, skipping sync to prevent mass inactivation", "provider", conn.ProviderID)
		return nil
	}

	// Load existing entries once to resolve pricing in memory (avoid N Get
	// queries). Build a map keyed by entry ID.
	existing := map[string]*domain.ModelEntry{}
	if cur, err := s.Models.ListByProvider(ctx, conn.ProviderID); err == nil {
		for i := range cur {
			existing[cur[i].ID] = &cur[i]
		}
	}

	now := time.Now()
	activeIDs := make([]string, 0, len(fetched))
	batch := make([]*domain.ModelEntry, 0, len(fetched))
	for _, m := range fetched {
		kind := s.resolveKind(m)
		// The metadata chain: what the provider said about its own model comes
		// first, and the external registries are consulted only for the fields
		// it left out.
		meta := s.modelMetadata(conn.ProviderID, m)
		reasoningCaps := inferReasoningCapabilities(m.ID)
		if s.Registry != nil {
			if registered, ok := s.Registry.ResolveReasoningCapabilitiesForProvider(conn.ProviderID, m.ID); ok {
				reasoningCaps = registered
			}
		}
		if meta.SupportsReasoning {
			reasoningCaps.SupportsReasoning = true
		}
		entry := &domain.ModelEntry{
			ID:                             conn.ProviderID + "/" + m.ID,
			ProviderID:                     conn.ProviderID,
			ModelID:                        m.ID,
			Name:                           m.ID,
			Kind:                           kind,
			Source:                         "sync",
			IsActive:                       true,
			Context:                        meta.Context,
			MaxOutputTokens:                meta.MaxOutputTokens,
			SupportsVision:                 meta.SupportsVision,
			SupportsToolCall:               meta.SupportsToolCall,
			SupportsReasoning:              reasoningCaps.SupportsReasoning || meta.SupportsReasoning,
			SupportsMinimalReasoningEffort: reasoningCaps.SupportsMinimalReasoningEffort,
			SupportsLowReasoningEffort:     reasoningCaps.SupportsLowReasoningEffort,
			SupportsXHighReasoningEffort:   reasoningCaps.SupportsXHighReasoningEffort,
			SupportsMaxReasoningEffort:     reasoningCaps.SupportsMaxReasoningEffort,
			LastSyncedAt:                   now,
			UpdatedAt:                      now,
		}
		// Resolve pricing: preserve manual overrides; otherwise ask the
		// registry; if neither has data, keep the existing DB pricing.
		if prev, ok := existing[entry.ID]; ok {
			// Preserve manual entries: if the user manually added or
			// toggled this model, don't let sync override its source or
			// active state.
			if prev.Source == "manual" {
				entry.Source = "manual"
				entry.IsActive = prev.IsActive
			}
			if prev.Pricing.Source == "manual" {
				entry.Pricing = prev.Pricing
			} else if s.Registry != nil {
				if pricing, ok := s.Registry.ResolvePricing(conn.ProviderID, m.ID); ok {
					entry.Pricing = pricing
				} else {
					entry.Pricing = prev.Pricing
				}
			} else {
				entry.Pricing = prev.Pricing
			}
			entry.CreatedAt = prev.CreatedAt
		} else if s.Registry != nil {
			if pricing, ok := s.Registry.ResolvePricing(conn.ProviderID, m.ID); ok {
				entry.Pricing = pricing
			}
		}
		batch = append(batch, entry)
		activeIDs = append(activeIDs, entry.ID)
	}

	if err := s.Models.UpsertBatch(ctx, batch); err != nil {
		slog.Error("model sync: batch upsert failed", "provider", conn.ProviderID, "err", err)
		return err
	}

	// Deactivate sync-sourced models that disappeared from the provider.
	// Only do this when the API actually returned models. If we're
	// operating on catalog-preset fallback data (API returned empty),
	// deactivating would wipe out real models that the API simply didn't
	// list this time around.
	if apiReturnedModels {
		if err := s.Models.DeactivateStaleSync(ctx, conn.ProviderID, activeIDs); err != nil {
			slog.Warn("model sync: deactivate stale failed", "provider", conn.ProviderID, "err", err)
		}
		// Reactivate sync-source models that reappeared in the API
		// response (they may have been deactivated on a previous sync
		// when the provider temporarily stopped listing them).
		if err := s.Models.ReactivateSync(ctx, conn.ProviderID, activeIDs); err != nil {
			slog.Warn("model sync: reactivate failed", "provider", conn.ProviderID, "err", err)
		}
	}
	slog.Info("model sync: provider synced", "provider", conn.ProviderID, "models", len(fetched))
	if s.OnSynced != nil {
		s.OnSynced(ctx)
	}
	return nil
}

// modelMetadata assembles what is known about a model, in chain order: the
// provider's own statement first, then the external registries for whatever it
// did not state. Later links only fill gaps — never overwrite.
func (s *ModelSyncService) modelMetadata(providerID string, m domain.ModelInfo) domain.ModelMetadata {
	meta := m.Metadata
	if s.Registry == nil {
		return meta
	}
	// The provider-specific entry is preferred over the model-only one: the
	// same model name can expose different limits through different upstreams.
	meta = fillMissingMetadata(meta, s.Registry.ResolveMetadataForProvider(providerID, m.ID))
	return fillMissingMetadata(meta, s.Registry.ResolveMetadata(m.ID))
}

// fillMissingMetadata keeps every field base already states and takes the rest
// from fallback. A zero field means "not stated", so a source that knows less
// never erases a fact another source established.
func fillMissingMetadata(base, fallback domain.ModelMetadata) domain.ModelMetadata {
	if base.Context == 0 {
		base.Context = fallback.Context
	}
	if base.MaxOutputTokens == 0 {
		base.MaxOutputTokens = fallback.MaxOutputTokens
	}
	base.SupportsVision = base.SupportsVision || fallback.SupportsVision
	base.SupportsToolCall = base.SupportsToolCall || fallback.SupportsToolCall
	base.SupportsReasoning = base.SupportsReasoning || fallback.SupportsReasoning
	return base
}

// resolveKind determines the ModelKind for a fetched model. Priority:
//  1. Provider's own metadata (model_type/endpoint_format in the /v1/models JSON)
//     — the provider is the source of truth for which endpoint to call.
//  2. External registries (LiteLLM, models.dev, OpenRouter via ModelRegistry)
//  3. Name heuristic
func (s *ModelSyncService) resolveKind(m domain.ModelInfo) domain.ModelKind {
	providerKind := m.Kind
	if providerKind != "" && providerKind != domain.KindLLM {
		return providerKind
	}
	if s.Registry != nil {
		if regKind, _, _, _, _ := s.Registry.ResolveKind(m.ID); regKind != "" {
			return regKind
		}
	}
	if providerKind != "" {
		return providerKind
	}
	return heuristicKind(m.ID)
}
