// Package semanticcache provides an in-memory vector-similarity cache for
// LLM responses. Entries are keyed by "{modelID}/{inputFormat}" and each
// holds an embedding vector. Lookup computes cosine similarity against all
// entries under the same key and returns the best match above threshold.
package semanticcache

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// memoryCache implements domain.SemanticCache with LRU eviction, per-entry
// TTL, and a background sweep goroutine. Lookups are linear scans within a
// key's entry set — acceptable for hundreds to low-thousands of entries.
type memoryCache struct {
	mu            sync.Mutex
	buckets       map[string][]semanticEntry
	maxEntries    int
	ttl           time.Duration
	sweepInterval time.Duration

	hits   atomic.Int64
	misses atomic.Int64

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type semanticEntry struct {
	resp      *domain.CachedResponse
	embedding []float32
	createdAt time.Time
	norm      float64 // precomputed L2 norm of embedding
}

// NewMemory returns an in-memory SemanticCache with the given maxEntries
// (LRU bound across all keys) and per-entry TTL. A background goroutine
// sweeps expired entries every sweepInterval. Call Close to stop it.
func NewMemory(maxEntries int, ttl, sweepInterval time.Duration) domain.SemanticCache {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if sweepInterval <= 0 {
		sweepInterval = time.Minute
	}
	c := &memoryCache{
		buckets:       make(map[string][]semanticEntry),
		maxEntries:    maxEntries,
		ttl:           ttl,
		sweepInterval: sweepInterval,
		stopCh:        make(chan struct{}),
	}
	c.wg.Add(1)
	go c.sweepLoop()
	return c
}

func (c *memoryCache) Get(_ context.Context, key string, embedding []float32, threshold float64) (*domain.CachedResponse, float64, bool) {
	queryNorm := l2Norm(embedding)
	if queryNorm == 0 {
		c.misses.Add(1)
		return nil, 0, false
	}

	c.mu.Lock()
	entries, ok := c.buckets[key]
	if !ok || len(entries) == 0 {
		c.mu.Unlock()
		c.misses.Add(1)
		return nil, 0, false
	}

	now := time.Now()
	var bestResp *domain.CachedResponse
	var bestSim float64 = -1
	var bestIdx int = -1

	for i, e := range entries {
		if now.After(e.createdAt.Add(c.ttl)) {
			continue
		}
		if e.norm == 0 {
			continue
		}
		sim := cosineSimilarity(embedding, queryNorm, e.embedding, e.norm)
		if sim > bestSim {
			bestSim = sim
			bestResp = e.resp
			bestIdx = i
		}
	}

	if bestIdx >= 0 && bestSim >= threshold {
		// Move best entry to front (LRU: most recently used).
		entries[0], entries[bestIdx] = entries[bestIdx], entries[0]
		c.buckets[key] = entries
		c.mu.Unlock()
		c.hits.Add(1)
		return bestResp, bestSim, true
	}

	c.mu.Unlock()
	c.misses.Add(1)
	return nil, 0, false
}

func (c *memoryCache) Put(_ context.Context, key string, embedding []float32, resp *domain.CachedResponse) {
	norm := l2Norm(embedding)
	if norm == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	bucket := c.buckets[key]
	// If this exact response is already cached (same pointer), skip.
	for _, e := range bucket {
		if e.resp == resp {
			return
		}
	}
	bucket = append(bucket, semanticEntry{
		resp:      resp,
		embedding: embedding,
		createdAt: time.Now(),
		norm:      norm,
	})
	c.buckets[key] = bucket

	// Global LRU eviction: if total entries exceed max, evict oldest from
	// the largest bucket.
	total := c.totalEntriesLocked()
	if total > c.maxEntries {
		c.evictOldestLocked()
	}
}

func (c *memoryCache) Flush(_ context.Context) {
	c.mu.Lock()
	c.buckets = make(map[string][]semanticEntry)
	c.mu.Unlock()
}

func (c *memoryCache) Stats() domain.CacheStats {
	c.mu.Lock()
	entries := c.totalEntriesLocked()
	c.mu.Unlock()
	return domain.CacheStats{
		Entries: entries,
		Hits:    c.hits.Load(),
		Misses:  c.misses.Load(),
	}
}

func (c *memoryCache) Close() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
	c.wg.Wait()
}

func (c *memoryCache) totalEntriesLocked() int {
	total := 0
	for _, bucket := range c.buckets {
		total += len(bucket)
	}
	return total
}

func (c *memoryCache) evictOldestLocked() {
	var oldestKey string
	var oldestIdx int
	var oldestTime time.Time
	for key, bucket := range c.buckets {
		for i, e := range bucket {
			if oldestTime.IsZero() || e.createdAt.Before(oldestTime) {
				oldestTime = e.createdAt
				oldestKey = key
				oldestIdx = i
			}
		}
	}
	if oldestKey != "" {
		bucket := c.buckets[oldestKey]
		bucket[oldestIdx] = bucket[len(bucket)-1]
		bucket = bucket[:len(bucket)-1]
		if len(bucket) == 0 {
			delete(c.buckets, oldestKey)
		} else {
			c.buckets[oldestKey] = bucket
		}
	}
}

func (c *memoryCache) sweepLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.sweep()
		}
	}
}

func (c *memoryCache) sweep() {
	now := time.Now()
	c.mu.Lock()
	for key, bucket := range c.buckets {
		var kept []semanticEntry
		for _, e := range bucket {
			if now.Before(e.createdAt.Add(c.ttl)) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(c.buckets, key)
		} else if len(kept) != len(bucket) {
			c.buckets[key] = kept
		}
	}
	c.mu.Unlock()
}

// l2Norm returns the Euclidean norm of a vector.
func l2Norm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

// cosineSimilarity computes the cosine similarity between two vectors.
// Precomputed norms are passed to avoid redundant sqrt calls.
func cosineSimilarity(a []float32, aNorm float64, b []float32, bNorm float64) float64 {
	if len(a) != len(b) || aNorm == 0 || bNorm == 0 {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot / (aNorm * bNorm)
}
