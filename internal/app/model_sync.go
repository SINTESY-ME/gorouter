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
// name heuristic), and upserts entries. Models that were sync-sourced and are
// no longer returned are deactivated.
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
		slog.Warn("model sync: no models returned by provider, skipping deactivation to prevent mass deletion", "provider", conn.ProviderID)
		return nil
	}

	now := time.Now()
	activeIDs := make([]string, 0, len(fetched))
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
		// Resolve pricing from the registry (LiteLLM → OpenRouter → models.dev
		// with fuzzy fallback). Preserve manual overrides; if the registry
		// doesn't return pricing, keep the existing DB pricing rather than
		// overwriting with an empty value.
		if existing, err := s.Models.Get(ctx, entry.ID); err == nil {
			if existing.Pricing.Source == "manual" {
				entry.Pricing = existing.Pricing
			} else if s.Registry != nil {
				if pricing, ok := s.Registry.ResolvePricing(conn.ProviderID, m.ID); ok {
					entry.Pricing = pricing
				} else {
					entry.Pricing = existing.Pricing
				}
			} else {
				entry.Pricing = existing.Pricing
			}
		} else if s.Registry != nil {
			if pricing, ok := s.Registry.ResolvePricing(conn.ProviderID, m.ID); ok {
				entry.Pricing = pricing
			}
		}
		if err := s.Models.Upsert(ctx, entry); err != nil {
			slog.Warn("model sync: upsert failed", "model", entry.ID, "err", err)
			continue
		}
		activeIDs = append(activeIDs, entry.ID)
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