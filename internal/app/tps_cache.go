package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// probeStaleAfter is how long a probe-measured TPS value is considered
// fresh before the model should be re-probed. Models with real usage data
// (from the DB) are never re-probed — usage data is continuously refreshed.
const probeStaleAfter = 1 * time.Hour

// probeResult is a single TPS measurement taken by an active probe.
type probeResult struct {
	tps        float64
	measuredAt time.Time
}

// TPSCache holds observed tokens-per-second per model in memory so the
// velocity strategy can reorder a combo's models without hitting the
// database on the hot path. Keyed by lowercase "provider/model".
//
// Two data sources are merged:
//   - stats: aggregated from the usage_entries table (real traffic).
//     Refreshed lazily with a TTL.
//   - probes: measured by TPSProber for models with no usage data.
//     Fresh for probeStaleAfter; re-probed when stale.
//
// Get prefers a fresh probe result, then usage data, then 0 (unknown).
type TPSCache struct {
	Usage domain.UsageRepo
	ttl   time.Duration

	mu       sync.RWMutex
	stats    map[string]float64
	probes   map[string]probeResult
	loadedAt time.Time
}

// NewTPSCache constructs a TPS cache with the given refresh interval for
// the usage-data snapshot.
func NewTPSCache(usage domain.UsageRepo, ttl time.Duration) *TPSCache {
	return &TPSCache{Usage: usage, ttl: ttl, probes: map[string]probeResult{}}
}

// Get returns the best-known TPS for the given "provider/model", or 0
// when there is no data. Preference: fresh probe result > usage data > 0.
// Nil-safe: a nil cache returns 0.
func (c *TPSCache) Get(modelStr string) float64 {
	if c == nil {
		return 0
	}
	key := strings.ToLower(modelStr)
	c.mu.RLock()
	if c.stats == nil || time.Since(c.loadedAt) > c.ttl {
		c.mu.RUnlock()
		c.refresh(context.Background())
		c.mu.RLock()
	}
	// Prefer fresh probe data.
	if p, ok := c.probes[key]; ok && time.Since(p.measuredAt) < probeStaleAfter {
		c.mu.RUnlock()
		return p.tps
	}
	tps := c.stats[key]
	c.mu.RUnlock()
	return tps
}

// SetProbe stores a probe-measured TPS value with the current timestamp.
// Called by TPSProber after a successful measurement.
func (c *TPSCache) SetProbe(modelStr string, tps float64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.probes == nil {
		c.probes = map[string]probeResult{}
	}
	c.probes[strings.ToLower(modelStr)] = probeResult{tps: tps, measuredAt: time.Now()}
	c.mu.Unlock()
}

// NeedsProbe reports whether the model should be probed: true when the
// model has no usage data AND (no probe result OR probe result is stale).
// Models with real usage data are never probed — their TPS is kept fresh
// by the periodic DB refresh.
func (c *TPSCache) NeedsProbe(modelStr string) bool {
	if c == nil {
		return false
	}
	key := strings.ToLower(modelStr)
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Has real usage data → no probe needed.
	if c.stats[key] > 0 {
		return false
	}
	// Has fresh probe data → no probe needed.
	if p, ok := c.probes[key]; ok && time.Since(p.measuredAt) < probeStaleAfter {
		return false
	}
	return true
}

// Refresh forces a reload of usage data from the database. Called at
// startup and may be invoked after usage is recorded to keep the cache warm.
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
