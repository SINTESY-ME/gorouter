package app

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/jhon/gorouter/internal/domain"
)

// ConnectionSelector owns the provider config cache and the round-robin
// rotation counter. It provides the starting index for connection
// iteration and a nil-safe config lookup for the hot path.
type ConnectionSelector struct {
	Providers domain.ProviderConfigRepo
	mu        sync.RWMutex
	cache     map[string]*domain.ProviderConfig
	rotation  uint32
}

func NewConnectionSelector(providers domain.ProviderConfigRepo) *ConnectionSelector {
	return &ConnectionSelector{Providers: providers}
}

// Config returns the provider config for the given provider ID, or a
// default openai-format config if the cache is empty. Nil-safe.
func (c *ConnectionSelector) Config(providerID string) *domain.ProviderConfig {
	c.mu.RLock()
	cfg := c.cache[providerID]
	c.mu.RUnlock()
	if cfg == nil {
		return &domain.ProviderConfig{ID: providerID, Format: domain.FormatOpenAI}
	}
	return cfg
}

// StartIndex returns the starting index for iterating connections. If the
// provider's strategy is "round-robin", it returns an atomically
// incremented index. Otherwise returns 0 (failover).
func (c *ConnectionSelector) StartIndex(conns []domain.Connection) int {
	if len(conns) == 0 {
		return 0
	}
	cfg := c.Config(conns[0].ProviderID)
	if cfg.LoadBalance == "round-robin" {
		return int(atomic.AddUint32(&c.rotation, 1)) % len(conns)
	}
	return 0
}

// Refresh loads all provider metadata from the database into the cache.
// Called at startup and after provider changes.
func (c *ConnectionSelector) Refresh(ctx context.Context) {
	if c.Providers == nil {
		return
	}
	ps, err := c.Providers.List(ctx)
	if err != nil {
		slog.Error("provider cache refresh failed", "err", err)
		return
	}
	m := make(map[string]*domain.ProviderConfig, len(ps))
	for i := range ps {
		p := &ps[i]
		if p.LoadBalance == "" {
			p.LoadBalance = "failover"
		}
		m[p.ID] = p
	}
	c.mu.Lock()
	c.cache = m
	c.mu.Unlock()
}