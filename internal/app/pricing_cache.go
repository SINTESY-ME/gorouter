package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/jhon/gorouter/internal/domain"
)

// PricingCache holds model pricing and context-window data in memory so the
// hot path does no DB lookup. Keyed by lowercase "provider/model". Refreshed
// at startup and after each model sync.
type PricingCache struct {
	Models   domain.ModelRepo
	mu       sync.RWMutex
	cache    map[string]domain.ModelPricing
	contexts map[string]int
}

func NewPricingCache(models domain.ModelRepo) *PricingCache {
	return &PricingCache{Models: models}
}

// Get returns the pricing for the given model, or (zero, false) if absent.
func (p *PricingCache) Get(m domain.ModelID) (domain.ModelPricing, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cache == nil {
		return domain.ModelPricing{}, false
	}
	pricing, ok := p.cache[strings.ToLower(m.Provider+"/"+m.Model)]
	return pricing, ok
}

// Context returns the model's context window in tokens, or (0, false) when
// unknown. Used by the combo router to skip models whose window can't fit the
// prompt.
func (p *PricingCache) Context(m domain.ModelID) (int, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.contexts == nil {
		return 0, false
	}
	ctx, ok := p.contexts[strings.ToLower(m.Provider+"/"+m.Model)]
	return ctx, ok
}

// Refresh reloads all model entries from the database. Models without
// pricing data are skipped (context data is always kept).
func (p *PricingCache) Refresh(ctx context.Context) {
	if p.Models == nil {
		return
	}
	entries, err := p.Models.List(ctx)
	if err != nil {
		slog.Error("pricing cache refresh failed", "err", err)
		return
	}
	pricing := make(map[string]domain.ModelPricing, len(entries))
	contexts := make(map[string]int, len(entries))
	for _, e := range entries {
		key := strings.ToLower(e.ID)
		if HasPricingData(e.Pricing) {
			pricing[key] = e.Pricing
		}
		if e.Context > 0 {
			contexts[key] = e.Context
		}
	}
	p.mu.Lock()
	p.cache = pricing
	p.contexts = contexts
	p.mu.Unlock()
	slog.Info("pricing cache refreshed", "models", len(pricing))
}
