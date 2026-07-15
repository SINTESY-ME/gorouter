package app

import "sync/atomic"

// SavingsStats holds cumulative token/byte savings from the response cache
// and RTK request compression. All counters are process-lifetime (reset on
// restart). Token estimates for RTK use ~4 bytes/token (English text average).
type SavingsStats struct {
	CacheHits        int64 `json:"cache_hits"`
	CacheTokensSaved int64 `json:"cache_tokens_saved"`
	RTKCompressions  int64 `json:"rtk_compressions"`
	RTKBytesSaved    int64 `json:"rtk_bytes_saved"`
	RTKTokensSaved   int64 `json:"rtk_tokens_saved"`
}

// SavingsTracker accumulates cache hit and RTK compression savings using
// atomic counters. It is safe for concurrent use. A nil tracker disables
// tracking (all methods are no-ops on nil).
type SavingsTracker struct {
	cacheHits        atomic.Int64
	cacheTokensSaved atomic.Int64
	rtkCompressions  atomic.Int64
	rtkBytesSaved    atomic.Int64
}

// NewSavingsTracker returns a ready tracker.
func NewSavingsTracker() *SavingsTracker { return &SavingsTracker{} }

// RecordCacheHit adds one cache hit and the tokens that were saved (prompt +
// completion from the cached response). No-op when t is nil.
func (t *SavingsTracker) RecordCacheHit(tokensSaved int) {
	if t == nil {
		return
	}
	t.cacheHits.Add(1)
	t.cacheTokensSaved.Add(int64(tokensSaved))
}

// RecordRTKCompression adds one RTK compression event and the bytes saved.
// No-op when t is nil.
func (t *SavingsTracker) RecordRTKCompression(bytesSaved int) {
	if t == nil {
		return
	}
	t.rtkCompressions.Add(1)
	t.rtkBytesSaved.Add(int64(bytesSaved))
}

// Stats returns a snapshot of cumulative savings.
func (t *SavingsTracker) Stats() SavingsStats {
	if t == nil {
		return SavingsStats{}
	}
	bytesSaved := t.rtkBytesSaved.Load()
	return SavingsStats{
		CacheHits:        t.cacheHits.Load(),
		CacheTokensSaved: t.cacheTokensSaved.Load(),
		RTKCompressions:  t.rtkCompressions.Load(),
		RTKBytesSaved:    bytesSaved,
		RTKTokensSaved:   bytesSaved / 4,
	}
}