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

// BudgetService checks whether an API key has exceeded its spending cap.
// It wraps a costSource with a short-TTL in-memory cache so the hot path
// (every /v1 request) doesn't hit the database on each call.
//
// Budget periods:
//   - "" (empty): no limit, always allowed
//   - "daily":   resets at 00:00 local time
//   - "monthly": resets on the 1st of the month at 00:00 local time
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

// Check reports whether the API key is within its budget. When limit <= 0
// or period is empty, the key is always allowed. Otherwise the cumulative
// cost since the period start is compared against the limit. The result
// is cached for ttl to avoid a DB query on every request.
func (bs *BudgetService) Check(ctx context.Context, apiKey string, limit float64, period string) BudgetResult {
	if limit <= 0 || period == "" {
		return BudgetResult{Allowed: true}
	}

	cacheKey := apiKey + "|" + period
	now := time.Now()

	bs.mu.RLock()
	if entry, ok := bs.cache[cacheKey]; ok && now.Sub(entry.cachedAt) < bs.ttl {
		bs.mu.RUnlock()
		allowed := entry.spent < limit
		return BudgetResult{
			Allowed: allowed,
			Spent:   entry.spent,
			Limit:   limit,
			ResetAt: budgetPeriodStart(period, now),
		}
	}
	bs.mu.RUnlock()

	since := budgetPeriodStart(period, now)
	spent, err := bs.usage.SumCostByApiKey(ctx, apiKey, since)
	if err != nil {
		return BudgetResult{Allowed: true}
	}

	bs.mu.Lock()
	bs.cache[cacheKey] = budgetCacheEntry{spent: spent, cachedAt: now}
	bs.mu.Unlock()

	allowed := spent < limit
	return BudgetResult{
		Allowed: allowed,
		Spent:   spent,
		Limit:   limit,
		ResetAt: since.Add(budgetPeriodDuration(period)),
	}
}

// Invalidate removes the cached spent amount for a key. Call this after
// recording a usage entry so the next Check reflects the new spend.
func (bs *BudgetService) Invalidate(apiKey, period string) {
	if apiKey == "" || period == "" {
		return
	}
	bs.mu.Lock()
	delete(bs.cache, apiKey+"|"+period)
	bs.mu.Unlock()
}

// budgetPeriodStart returns the start of the current budget period.
func budgetPeriodStart(period string, now time.Time) time.Time {
	switch period {
	case "daily":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "monthly":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	default:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
}

// budgetPeriodDuration returns the duration of one full budget cycle.
func budgetPeriodDuration(period string) time.Duration {
	switch period {
	case "monthly":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}
