package app

import (
	"context"
	"sync"
	"time"
)

// costSource supplies the cumulative spend for an API key since a timestamp.
// A narrow interface so BudgetService only depends on what it needs (the
// full UsageRepo carries unrelated query surface).
type costSource interface {
	SumCostByApiKey(ctx context.Context, apiKey string, since time.Time) (float64, error)
}

// BudgetService checks whether an API key has exceeded a spend cap over a
// rolling window. It wraps a costSource with a short-TTL in-memory cache so
// the hot path (every /v1 request) doesn't hit the database on each call.
// A limit of 0 or less means no cap (always allowed).
type BudgetService struct {
	usage costSource
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]budgetCacheEntry
}

type budgetCacheEntry struct {
	spent    float64
	cachedAt time.Time
}

// NewBudgetService returns a BudgetService backed by the given cost source.
// The in-memory cache TTL defaults to 30 seconds.
func NewBudgetService(usage costSource) *BudgetService {
	return &BudgetService{
		usage: usage,
		ttl:   30 * time.Second,
		cache: make(map[string]budgetCacheEntry),
	}
}

// BudgetResult holds the result of a budget check.
type BudgetResult struct {
	Allowed bool
	Spent   float64
	Limit   float64
	ResetAt time.Time
}

// Check reports whether the key's spend since (now - duration) is within
// limit. When limit <= 0 the key is always allowed. The result is cached for
// ttl per (key, duration) so the rolling sum isn't recomputed on every
// request. ResetAt marks when the current window rolls over (now, for a
// rolling window) — used for Retry-After on a blocked request.
func (bs *BudgetService) Check(ctx context.Context, apiKey string, limit float64, duration time.Duration) BudgetResult {
	if limit <= 0 || duration <= 0 {
		return BudgetResult{Allowed: true}
	}

	cacheKey := apiKey + "|" + duration.String()
	now := time.Now()

	bs.mu.RLock()
	if entry, ok := bs.cache[cacheKey]; ok && now.Sub(entry.cachedAt) < bs.ttl {
		bs.mu.RUnlock()
		return BudgetResult{
			Allowed: entry.spent < limit,
			Spent:   entry.spent,
			Limit:   limit,
			ResetAt: now,
		}
	}
	bs.mu.RUnlock()

	since := now.Add(-duration)
	spent, err := bs.usage.SumCostByApiKey(ctx, apiKey, since)
	if err != nil {
		return BudgetResult{Allowed: true}
	}

	bs.mu.Lock()
	bs.cache[cacheKey] = budgetCacheEntry{spent: spent, cachedAt: now}
	bs.mu.Unlock()

	return BudgetResult{
		Allowed: spent < limit,
		Spent:   spent,
		Limit:   limit,
		ResetAt: now,
	}
}
