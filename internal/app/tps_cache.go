package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// TPSCache holds observed tokens-per-second per model in memory so the
// velocity strategy can reorder a combo's models without hitting the
// database on the hot path. Keyed by lowercase "provider/model". It
// refreshes lazily with a TTL: the first request after the snapshot
// stales triggers a single aggregated query, subsequent requests within
// the TTL read from the in-memory map (lock-free via RWMutex).
type TPSCache struct {
	Usage domain.UsageRepo
	ttl   time.Duration

	mu       sync.RWMutex
	stats    map[string]float64
	loadedAt time.Time
}

// NewTPSCache constructs a TPS cache with the given refresh interval.
func NewTPSCache(usage domain.UsageRepo, ttl time.Duration) *TPSCache {
	return &TPSCache{Usage: usage, ttl: ttl}
}

// Get returns the observed average TPS for the given "provider/model",
// or 0 when there is no data yet. Nil-safe: a nil cache returns 0.
func (c *TPSCache) Get(modelStr string) float64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	if c.stats == nil || time.Since(c.loadedAt) > c.ttl {
		c.mu.RUnlock()
		c.refresh(context.Background())
		c.mu.RLock()
	}
	tps := c.stats[strings.ToLower(modelStr)]
	c.mu.RUnlock()
	return tps
}

// Refresh forces a reload from the database. Called at startup and may be
// invoked after usage is recorded to keep the cache warm.
func (c *TPSCache) Refresh(ctx context.Context) {
	c.refresh(ctx)
}

func (c *TPSCache) refresh(ctx context.Context) {
	if c.Usage == nil {
		return
	}
	stats, err := c.Usage.ModelStatsByID(ctx)
	if err != nil {
		slog.Warn("tps cache refresh failed", "err", err)
		return
	}
	m := make(map[string]float64, len(stats))
	for id, s := range stats {
		if s != nil && s.AvgTPS > 0 {
			m[strings.ToLower(id)] = s.AvgTPS
		}
	}
	c.mu.Lock()
	c.stats = m
	c.loadedAt = time.Now()
	c.mu.Unlock()
}
