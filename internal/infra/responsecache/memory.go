// Package responsecache provides an in-memory LRU+TTL response cache for
// the gorouter direct-hash cache. It uses a map + doubly-linked list for
// O(1) LRU eviction and a background sweeper for expired entries.
package responsecache

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// memoryCache implements domain.ResponseCache with LRU eviction, per-entry
// TTL, and a background sweep goroutine. Lookups on expired entries are
// treated as misses and the entry is removed asynchronously.
type memoryCache struct {
	mu        sync.Mutex
	entries   map[string]*list.Element
	ll        *list.List
	maxEntries int
	ttl       time.Duration
	sweepInterval time.Duration

	hits   atomic.Int64
	misses atomic.Int64

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type cacheEntry struct {
	key       string
	resp      *domain.CachedResponse
	expiresAt time.Time
}

// NewMemory returns an in-memory ResponseCache with the given maxEntries (LRU
// bound) and per-entry TTL. A background goroutine sweeps expired entries
// every sweepInterval. Call Close to stop the sweeper.
func NewMemory(maxEntries int, ttl, sweepInterval time.Duration) domain.ResponseCache {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if sweepInterval <= 0 {
		sweepInterval = time.Minute
	}
	c := &memoryCache{
		entries:       make(map[string]*list.Element),
		ll:            list.New(),
		maxEntries:    maxEntries,
		ttl:           ttl,
		sweepInterval: sweepInterval,
		stopCh:        make(chan struct{}),
	}
	c.wg.Add(1)
	go c.sweepLoop()
	return c
}

func (c *memoryCache) Get(_ context.Context, key string) (*domain.CachedResponse, bool) {
	c.mu.Lock()
	elem, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		c.misses.Add(1)
		return nil, false
	}
	e := elem.Value.(*cacheEntry)
	if time.Now().After(e.expiresAt) {
		c.removeElement(elem)
		c.mu.Unlock()
		c.misses.Add(1)
		return nil, false
	}
	c.ll.MoveToFront(elem)
	c.mu.Unlock()
	c.hits.Add(1)
	return e.resp, true
}

func (c *memoryCache) Put(_ context.Context, key string, resp *domain.CachedResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		e := elem.Value.(*cacheEntry)
		e.resp = resp
		e.expiresAt = time.Now().Add(c.ttl)
		c.ll.MoveToFront(elem)
		return
	}
	e := &cacheEntry{
		key:       key,
		resp:      resp,
		expiresAt: time.Now().Add(c.ttl),
	}
	elem := c.ll.PushFront(e)
	c.entries[key] = elem
	if c.ll.Len() > c.maxEntries {
		oldest := c.ll.Back()
		if oldest != nil {
			c.removeElement(oldest)
		}
	}
}

func (c *memoryCache) Delete(_ context.Context, key string) {
	c.mu.Lock()
	if elem, ok := c.entries[key]; ok {
		c.removeElement(elem)
	}
	c.mu.Unlock()
}

func (c *memoryCache) Flush(_ context.Context) {
	c.mu.Lock()
	c.entries = make(map[string]*list.Element)
	c.ll = list.New()
	c.mu.Unlock()
}

func (c *memoryCache) Stats() domain.CacheStats {
	c.mu.Lock()
	entries := c.ll.Len()
	c.mu.Unlock()
	return domain.CacheStats{
		Entries: entries,
		Hits:    c.hits.Load(),
		Misses:  c.misses.Load(),
	}
}

func (c *memoryCache) removeElement(elem *list.Element) {
	e := elem.Value.(*cacheEntry)
	delete(c.entries, e.key)
	c.ll.Remove(elem)
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
	var toRemove []*list.Element
	for elem := c.ll.Back(); elem != nil; elem = elem.Prev() {
		e := elem.Value.(*cacheEntry)
		if now.After(e.expiresAt) {
			toRemove = append(toRemove, elem)
		}
	}
	for _, elem := range toRemove {
		c.removeElement(elem)
	}
	c.mu.Unlock()
}

// Close stops the background sweep goroutine. Safe to call multiple times.
func (c *memoryCache) Close() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
	c.wg.Wait()
}