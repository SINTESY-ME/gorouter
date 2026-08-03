package app

import "sync"

// SavingsStats holds cumulative token/byte savings from the response cache
// and RTK request compression. All counters are process-lifetime (reset on
// restart). Token estimates for RTK use ~4 bytes/token (English text average).
// CostSaved fields are in USD, calculated from the PricingCache at the moment
// of the cache hit or compression.
type SavingsStats struct {
	CacheHits           int64   `json:"cache_hits"`
	CacheTokensSaved    int64   `json:"cache_tokens_saved"`
	CacheCostSaved      float64 `json:"cache_cost_saved"`
	RTKCompressions     int64   `json:"rtk_compressions"`
	RTKBytesSaved       int64   `json:"rtk_bytes_saved"`
	RTKTokensSaved      int64   `json:"rtk_tokens_saved"`
	RTKCostSaved        float64 `json:"rtk_cost_saved"`
	SemanticHits        int64   `json:"semantic_hits"`
	SemanticTokensSaved int64   `json:"semantic_tokens_saved"`
	SemanticCostSaved   float64 `json:"semantic_cost_saved"`
}

// SavingsTracker accumulates cache hit and RTK compression savings. It is
// safe for concurrent use. A nil tracker disables tracking (all methods are
// no-ops on nil).
type SavingsTracker struct {
	mu                  sync.Mutex
	cacheHits           int64
	cacheTokensSaved    int64
	cacheCostSaved      float64
	rtkCompressions     int64
	rtkBytesSaved       int64
	rtkCostSaved        float64
	semanticHits        int64
	semanticTokensSaved int64
	semanticCostSaved   float64
}

// NewSavingsTracker returns a ready tracker.
func NewSavingsTracker() *SavingsTracker { return &SavingsTracker{} }

// RecordCacheHit adds one cache hit, the tokens saved, and the real cost
// saved (USD) computed from the model's pricing. costSaved is 0 when
// pricing is unavailable. No-op when t is nil.
func (t *SavingsTracker) RecordCacheHit(tokensSaved int, costSaved float64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cacheHits++
	t.cacheTokensSaved += int64(tokensSaved)
	t.cacheCostSaved += costSaved
}

// RecordRTKCompression adds one RTK compression event, the bytes saved, and
// the real cost saved (USD) computed from the model's input token price.
// No-op when t is nil.
func (t *SavingsTracker) RecordRTKCompression(bytesSaved int, costSaved float64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rtkCompressions++
	t.rtkBytesSaved += int64(bytesSaved)
	t.rtkCostSaved += costSaved
}

// RecordSemanticCacheHit adds one semantic cache hit, the tokens saved, and
// the real cost saved (USD). No-op when t is nil.
func (t *SavingsTracker) RecordSemanticCacheHit(tokensSaved int, costSaved float64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.semanticHits++
	t.semanticTokensSaved += int64(tokensSaved)
	t.semanticCostSaved += costSaved
}

// Stats returns a snapshot of cumulative savings.
func (t *SavingsTracker) Stats() SavingsStats {
	if t == nil {
		return SavingsStats{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return SavingsStats{
		CacheHits:           t.cacheHits,
		CacheTokensSaved:    t.cacheTokensSaved,
		CacheCostSaved:      t.cacheCostSaved,
		RTKCompressions:     t.rtkCompressions,
		RTKBytesSaved:       t.rtkBytesSaved,
		RTKTokensSaved:      t.rtkBytesSaved / 4,
		RTKCostSaved:        t.rtkCostSaved,
		SemanticHits:        t.semanticHits,
		SemanticTokensSaved: t.semanticTokensSaved,
		SemanticCostSaved:   t.semanticCostSaved,
	}
}
