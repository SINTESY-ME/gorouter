package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
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
	// groups maps a model name to a caching-group ID. When a requested model
	// belongs to a group, the cache key uses the group ID so all models in
	// the group share the same entries. Nil disables grouping.
	groups map[string]string
}

// NewCacheService wraps a ResponseCache with key-computation helpers. A nil
// cache is valid and means caching is disabled; all methods are no-ops.
func NewCacheService(cache domain.ResponseCache) *CacheService {
	return &CacheService{cache: cache}
}

// SetCachingGroups enables model-group sharing: the map is model name →
// group ID (built from the caching_groups setting). Requests whose model is
// in the map share cache entries with every other model in the same group.
func (cs *CacheService) SetCachingGroups(groups map[string]string) {
	cs.groups = groups
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

// cacheTTLCtxKey is the context key for a per-request TTL (x-gr-cache-ttl).
// Zero means "use the global TTL".
type cacheTTLCtxKey struct{}

// WithCacheTTL marks the context with a per-request cache TTL. Entries stored
// for this request expire after ttl instead of the global default.
func WithCacheTTL(ctx context.Context, ttl time.Duration) context.Context {
	return context.WithValue(ctx, cacheTTLCtxKey{}, ttl)
}

// cacheTTLFromCtx returns the per-request TTL, or 0 when unset.
func cacheTTLFromCtx(ctx context.Context) time.Duration {
	ttl, _ := ctx.Value(cacheTTLCtxKey{}).(time.Duration)
	return ttl
}

// Enabled reports whether caching is active.
func (cs *CacheService) Enabled() bool {
	return cs != nil && cs.cache != nil
}

// ComputeKey returns a deterministic hash for the request body, model string,
// and input format. The body is normalized: ephemeral fields (user, request_id,
// n, seed metadata) are stripped and map keys are sorted recursively before
// hashing. When the model belongs to a caching group, the group ID replaces
// the model so group members share entries.
func (cs *CacheService) ComputeKey(body []byte, modelStr string, inputFormat domain.Format) string {
	normalized := normalizeBody(body)
	h := sha256.New()
	h.Write([]byte(cs.resolveGroup(modelStr)))
	h.Write([]byte(inputFormat))
	h.Write(normalized)
	return hex.EncodeToString(h.Sum(nil))
}

// resolveGroup returns the caching-group ID for a model when it belongs to a
// group, else the model string unchanged.
func (cs *CacheService) resolveGroup(modelStr string) string {
	if cs.groups == nil {
		return modelStr
	}
	if g, ok := cs.groups[modelStr]; ok {
		return "group:" + g
	}
	// Match the bare model name after the provider prefix (e.g. "gpt-4o"
	// from "openai/gpt-4o").
	if i := strings.LastIndexByte(modelStr, '/'); i >= 0 {
		if g, ok := cs.groups[modelStr[i+1:]]; ok {
			return "group:" + g
		}
	}
	return modelStr
}

// Lookup returns a cached response for the key, or nil on miss. Entries whose
// per-request ExpiresAt has passed are treated as misses and deleted
// best-effort.
func (cs *CacheService) Lookup(ctx context.Context, key string) (*domain.CachedResponse, bool) {
	resp, ok := cs.cache.Get(ctx, key)
	if !ok {
		return nil, false
	}
	if !resp.ExpiresAt.IsZero() && time.Now().After(resp.ExpiresAt) {
		cs.cache.Delete(ctx, key)
		return nil, false
	}
	return resp, true
}

// Store records a non-streaming JSON response in the cache. The entry's
// expiry comes from the per-request TTL (x-gr-cache-ttl) when set, else the
// backend's global TTL applies.
func (cs *CacheService) Store(ctx context.Context, key string, statusCode int, headers http.Header, body []byte) {
	cs.cache.Put(ctx, key, &domain.CachedResponse{
		StatusCode: statusCode,
		Headers:    headers,
		Body:       body,
		Stream:     false,
		CreatedAt:  time.Now(),
		ExpiresAt:  expiresAt(cacheTTLFromCtx(ctx)),
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
		ExpiresAt:    expiresAt(cacheTTLFromCtx(ctx)),
	})
}

// expiresAt returns now+ttl when ttl > 0, else the zero time (no per-entry
// override; the backend's global TTL applies).
func expiresAt(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
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
	// n is semantically a count: the default (1) must not change the key, but
	// n > 1 changes the response shape and must stay in the key.
	if n, ok := raw["n"].(float64); ok && n == 1 {
		delete(raw, "n")
	}
	return marshalSorted(raw)
}

// ephemeralFields are request fields that should not affect the cache key
// because they vary per request without changing the response.
var ephemeralFields = []string{
	"user",
	"request_id",
	"metadata",
	"idempotency_key",
}

// setArrayFields are request fields that are semantically sets encoded as
// arrays: their element order must not change the cache key. This matters for
// agents/MCP where tools arrive in nondeterministic order.
var setArrayFields = map[string]bool{
	"tools":      true,
	"stop":       true,
	"modalities": true,
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
			val := t[k]
			if setArrayFields[k] {
				if arr, ok := val.([]any); ok {
					val = sortJSONArray(arr)
				}
			}
			buf.Write(sortJSONValue(val))
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

// sortJSONArray returns a copy of arr sorted by each element's canonical JSON
// representation, making set-encoded fields (tools, stop) order-insensitive.
func sortJSONArray(arr []any) []any {
	out := make([]any, len(arr))
	for i, e := range arr {
		out[i] = e
	}
	sort.SliceStable(out, func(i, j int) bool {
		return bytes.Compare(sortJSONValue(out[i]), sortJSONValue(out[j])) < 0
	})
	return out
}

// messageCount returns the number of chat messages in the request body, or -1
// when the body is not a parseable chat request.
func messageCount(body []byte) int {
	var probe struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return -1
	}
	return len(probe.Messages)
}
