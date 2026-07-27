package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// ProbeFreshness is how long a probe-measured TPS is considered authoritative
// over the aggregated usage-derived TPS. After this window the prober is
// asked to re-measure (a background call). One hour is a pragmatic balance
// between capturing provider performance drift and not hammering upstreams.
const ProbeFreshness = 1 * time.Hour

// probeResult is a TPS value measured by an active probe call to the model.
type probeResult struct {
	tps        float64
	measuredAt time.Time
}

// TPSCache holds observed tokens-per-second per model in memory so the
// velocity strategy can reorder a combo's models without hitting the
// database on the hot path. Keyed by lowercase "provider/model".
//
// TPS comes from two sources, merged on read:
//   - "stats" — historical average computed from the usage_entries table,
//     refreshed lazily with a TTL on first access after expiry.
//   - "probes" — a single live measurement taken by the TPSProber. While
//     fresh (< ProbeFreshness) it overrides the historical average, since
//     it reflects current provider load/network conditions.
//
// The hot path (Get) is a single RWMutex RLock and a map lookup. Refresh
// from the DB is synchronous but only happens once per TTL window.
type TPSCache struct {
	Usage domain.UsageRepo
	ttl   time.Duration

	mu       sync.RWMutex
	stats    map[string]float64     // usage-derived avg TPS; refreshed lazily
	probes   map[string]probeResult // probe-measured TPS; takes precedence while fresh
	loadedAt time.Time
}

// NewTPSCache constructs a TPS cache with the given refresh interval.
func NewTPSCache(usage domain.UsageRepo, ttl time.Duration) *TPSCache {
	return &TPSCache{Usage: usage, ttl: ttl}
}

// Get returns the TPS for the given "provider/model", preferring a fresh
// probe measurement over historical usage data. Returns 0 when no data is
// available. Nil-safe: a nil cache returns 0.
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
	tps := c.stats[key]
	if p, ok := c.probes[key]; ok && time.Since(p.measuredAt) < ProbeFreshness && p.tps > 0 {
		tps = p.tps
	}
	c.mu.RUnlock()
	return tps
}

// SetProbe stores a live-measured TPS for the given model. Called by the
// TPSProber after a successful probe. The measurement is authoritative
// for the next ProbeFreshness window, after which the prober may re-test.
func (c *TPSCache) SetProbe(modelStr string, tps float64) {
	if c == nil {
		return
	}
	if tps <= 0 {
		return
	}
	key := strings.ToLower(modelStr)
	c.mu.Lock()
	c.probes[key] = probeResult{tps: tps, measuredAt: time.Now()}
	c.mu.Unlock()
}

// NeedsProbe reports whether the prober should run a measurement for the
// given model. A model needs probing when it has no usable TPS at all, or
// its last live measurement has aged beyond ProbeFreshness AND no recent
// usage data exists to lean on. Models with current real traffic are left
// alone — the historical average is good enough for them.
func (c *TPSCache) NeedsProbe(modelStr string) bool {
	if c == nil {
		return false
	}
	key := strings.ToLower(modelStr)
	c.mu.RLock()
	defer c.mu.RUnlock()
	usageTPS := c.stats[key]
	if usageTPS > 0 {
		return false // real traffic; stats are good enough
	}
	p, ok := c.probes[key]
	if !ok {
		return true // never measured
	}
	return time.Since(p.measuredAt) >= ProbeFreshness // stale probe
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
