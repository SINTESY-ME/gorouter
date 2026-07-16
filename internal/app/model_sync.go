package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/jhon/gorouter/internal/domain"
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
	if len(fetched) == 0 {
		// An empty list usually means a flaky provider API, not a real
		// removal of all models. Skipping deactivation prevents mass
		// catalog wipeouts on transient errors. The trade-off is that a
		// genuine full removal won't be reflected until the list comes
		// back non-empty.
		slog.Warn("model sync: no models returned by provider, skipping deactivation to prevent mass deletion", "provider", conn.ProviderID)
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
		kind, contextLen, vision, toolCall, reasoning := s.resolveKind(m)
		entry := &domain.ModelEntry{
			ID:                conn.ProviderID + "/" + m.ID,
			ProviderID:        conn.ProviderID,
			ModelID:           m.ID,
			Name:              m.ID,
			Kind:              kind,
			Source:            "sync",
			IsActive:          true,
			Context:           contextLen,
			SupportsVision:    vision,
			SupportsToolCall:  toolCall,
			SupportsReasoning: reasoning,
			LastSyncedAt:      now,
			UpdatedAt:         now,
		}
		// Resolve pricing: preserve manual overrides; otherwise ask the
		// registry; if neither has data, keep the existing DB pricing.
		if prev, ok := existing[entry.ID]; ok {
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
	if err := s.Models.DeactivateStaleSync(ctx, conn.ProviderID, activeIDs); err != nil {
		slog.Warn("model sync: deactivate stale failed", "provider", conn.ProviderID, "err", err)
	}
	slog.Info("model sync: provider synced", "provider", conn.ProviderID, "models", len(fetched))
	if s.OnSynced != nil {
		s.OnSynced(ctx)
	}
	return nil
}

// resolveKind determines the ModelKind for a fetched model. Priority:
// 1. Provider's own metadata (model_type/endpoint_format in the /v1/models JSON)
// 2. External registries (LiteLLM, models.dev, OpenRouter via ModelRegistry)
// 3. Name heuristic
func (s *ModelSyncService) resolveKind(m domain.ModelInfo) (domain.ModelKind, int, bool, bool, bool) {
	// If the fetcher already populated Kind from provider metadata, use it.
	if m.Kind != "" {
		return m.Kind, 0, false, false, false
	}
	// Try external registries + heuristic fallback.
	if s.Registry != nil {
		return s.Registry.ResolveKind(m.ID)
	}
	k := heuristicKind(m.ID)
	return k, 0, false, false, false
}