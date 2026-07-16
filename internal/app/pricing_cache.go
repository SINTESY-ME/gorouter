package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/jhon/gorouter/internal/domain"
)

// PricingCache holds model pricing data in memory so the hot path does no
// DB lookup. Keyed by lowercase "provider/model". Refreshed at startup and
// after each model sync.
type PricingCache struct {
	Models domain.ModelRepo
	mu     sync.RWMutex
	cache  map[string]domain.ModelPricing
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

// Refresh reloads all model entries from the database. Models without
// pricing data are skipped.
func (p *PricingCache) Refresh(ctx context.Context) {
	if p.Models == nil {
		return
	}
	entries, err := p.Models.List(ctx)
	if err != nil {
		slog.Error("pricing cache refresh failed", "err", err)
		return
	}
	m := make(map[string]domain.ModelPricing, len(entries))
	for _, e := range entries {
		if HasPricingData(e.Pricing) {
			m[strings.ToLower(e.ID)] = e.Pricing
		}
	}
	p.mu.Lock()
	p.cache = m
	p.mu.Unlock()
	slog.Info("pricing cache refreshed", "models", len(m))
}