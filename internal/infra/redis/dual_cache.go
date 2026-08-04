package redis

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// cachePrefix namespaces response-cache keys in Redis.
const cachePrefix = "cache"

// DualCache is a domain.ResponseCache that reads/writes both an in-memory
// cache (fast path, single instance) and Redis (shared across instances).
// Reads try memory first, then Redis, populating memory on a Redis hit. Writes
// go to both, so a cache hit on one instance serves every instance — the main
// multi-instance win. Fail-open: any Redis error leaves the in-memory layer
// fully functional.
type DualCache struct {
	mem domain.ResponseCache
	r   *Client
	ttl time.Duration
}

// NewDualCache wraps an in-memory ResponseCache with a shared Redis layer.
func NewDualCache(mem domain.ResponseCache, r *Client, ttl time.Duration) *DualCache {
	return &DualCache{mem: mem, r: r, ttl: ttl}
}

func (d *DualCache) cacheKey(key string) string { return cachePrefix + ":" + key }

// Get returns from memory first; on a miss it checks Redis and populates
// memory so warm instances stay on the fast path.
func (d *DualCache) Get(ctx context.Context, key string) (*domain.CachedResponse, bool) {
	if res, ok := d.mem.Get(ctx, key); ok {
		return res, true
	}
	raw, err := d.r.Get(ctx, d.cacheKey(key))
	if err != nil || raw == nil {
		return nil, false
	}
	var res domain.CachedResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, false
	}
	d.mem.Put(ctx, key, &res)
	return &res, true
}

// Put writes to both memory and Redis. A Redis write failure is logged by the
// client and ignored here.
func (d *DualCache) Put(ctx context.Context, key string, res *domain.CachedResponse) {
	d.mem.Put(ctx, key, res)
	if raw, err := json.Marshal(res); err == nil {
		_ = d.r.Set(ctx, d.cacheKey(key), raw, d.ttl)
	}
}

// Delete removes the key from both layers.
func (d *DualCache) Delete(ctx context.Context, key string) {
	d.mem.Delete(ctx, key)
	_ = d.r.Del(ctx, d.cacheKey(key))
}

// Flush clears memory and best-effort clears the Redis cache namespace.
func (d *DualCache) Flush(ctx context.Context) {
	d.mem.Flush(ctx)
	if keys, err := d.r.ScanKeys(ctx, cachePrefix+":*"); err == nil {
		if len(keys) > 0 {
			_ = d.r.Del(ctx, keys...)
		}
	}
}

// Stats reports local (in-memory) statistics.
func (d *DualCache) Stats() domain.CacheStats { return d.mem.Stats() }

// Close stops the in-memory cache. The Redis client is shared and closed by
// the composition root.
func (d *DualCache) Close() { d.mem.Close() }
