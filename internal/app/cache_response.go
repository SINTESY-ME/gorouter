package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// CacheService computes deterministic cache keys from request bodies and
// provides lookup/store helpers. It is used by the RouterService to
// short-circuit identical requests and to record responses for future hits.
//
// The key is derived from a normalized JSON representation of the request
// body (ephemeral fields stripped, map keys sorted) combined with the model
// and input format. Normalization ensures that two requests with the same
// semantic content produce the same key even if field ordering or transient
// metadata differs.
type CacheService struct {
	cache domain.ResponseCache
}

// NewCacheService wraps a ResponseCache with key-computation helpers. A nil
// cache is valid and means caching is disabled; all methods are no-ops.
func NewCacheService(cache domain.ResponseCache) *CacheService {
	return &CacheService{cache: cache}
}

// cacheDisabledCtxKey is the context key for per-request cache bypass.
type cacheDisabledCtxKey struct{}

// WithCacheDisabled marks the context to bypass the response cache (both
// lookup and store) for this request.
func WithCacheDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheDisabledCtxKey{}, true)
}

// isCacheDisabled reports whether the request context opted out of caching.
func isCacheDisabled(ctx context.Context) bool {
	v, _ := ctx.Value(cacheDisabledCtxKey{}).(bool)
	return v
}

// Enabled reports whether caching is active.
func (cs *CacheService) Enabled() bool {
	return cs != nil && cs.cache != nil
}

// ComputeKey returns a deterministic hash for the request body, model string,
// and input format. The body is normalized: ephemeral fields (user, request_id,
// n, seed metadata) are stripped and map keys are sorted recursively before
// hashing.
func (cs *CacheService) ComputeKey(body []byte, modelStr string, inputFormat domain.Format) string {
	normalized := normalizeBody(body)
	h := sha256.New()
	h.Write([]byte(modelStr))
	h.Write([]byte(inputFormat))
	h.Write(normalized)
	return hex.EncodeToString(h.Sum(nil))
}

// Lookup returns a cached response for the key, or nil on miss.
func (cs *CacheService) Lookup(ctx context.Context, key string) (*domain.CachedResponse, bool) {
	return cs.cache.Get(ctx, key)
}

// Store records a non-streaming JSON response in the cache.
func (cs *CacheService) Store(ctx context.Context, key string, statusCode int, headers http.Header, body []byte) {
	cs.cache.Put(ctx, key, &domain.CachedResponse{
		StatusCode: statusCode,
		Headers:    headers,
		Body:       body,
		Stream:     false,
		CreatedAt:  time.Now(),
	})
}

// StoreStream records a streaming response (concatenated SSE chunks) in the
// cache for later replay.
func (cs *CacheService) StoreStream(ctx context.Context, key string, statusCode int, headers http.Header, chunks []byte) {
	cs.cache.Put(ctx, key, &domain.CachedResponse{
		StatusCode:   statusCode,
		Headers:      headers,
		StreamChunks: chunks,
		Stream:       true,
		CreatedAt:    time.Now(),
	})
}

// Flush removes all cached entries.
func (cs *CacheService) Flush(ctx context.Context) {
	cs.cache.Flush(ctx)
}

// DeleteKey removes a single entry by key.
func (cs *CacheService) DeleteKey(ctx context.Context, key string) {
	cs.cache.Delete(ctx, key)
}

// Stats returns current cache statistics.
func (cs *CacheService) Stats() domain.CacheStats {
	return cs.cache.Stats()
}

// normalizeBody strips ephemeral fields from the request body and re-marshals
// with deterministic key ordering. If the body is not valid JSON it is hashed
// as-is (e.g. multipart bodies).
func normalizeBody(body []byte) []byte {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}
	for _, field := range ephemeralFields {
		delete(raw, field)
	}
	return marshalSorted(raw)
}

// ephemeralFields are request fields that should not affect the cache key
// because they vary per request without changing the response.
var ephemeralFields = []string{
	"user",
	"request_id",
	"n",
	"metadata",
	"idempotency_key",
}

// marshalSorted recursively sorts map keys to produce deterministic JSON.
func marshalSorted(v any) []byte {
	return sortJSONValue(v)
}

func sortJSONValue(v any) []byte {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			buf.Write(sortJSONValue(t[k]))
		}
		buf.WriteByte('}')
		return buf.Bytes()
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(sortJSONValue(e))
		}
		buf.WriteByte(']')
		return buf.Bytes()
	default:
		b, _ := json.Marshal(v)
		return b
	}
}

