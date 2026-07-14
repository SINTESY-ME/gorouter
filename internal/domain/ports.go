package domain

import (
	"context"
	"io"
	"net/http"
	"time"
)

// ExecuteRequest is a single upstream call, already resolved to a connection.
// The body is in the target provider's native format (translation, if any,
// has already been applied by the Translator). The executor's only job is to
// send the request and return the response — it is a thin, fast reverse proxy.
type ExecuteRequest struct {
	ProviderID  string
	Connection  *Connection
	UpstreamModel string // the model id at the upstream (after alias resolution)
	Body        io.ReadCloser // already in target format
	Stream      bool
	Headers     map[string]string // extra headers (e.g. anthropic-version)
	Endpoint    string // "" = chat (format-based URL); "embeddings" | "images/generations" force OpenAI-style paths
}

// ExecuteResult is the upstream's response. The caller owns closing Body.
// For streaming responses, Body yields SSE chunks in the upstream format;
// the Translator (if any) wraps it before writing to the client.
type ExecuteResult struct {
	StatusCode int
	Headers     http.Header
	Body        io.ReadCloser
	Stream      bool
}

// Executor makes a single upstream HTTP call. Implementations must not
// buffer streaming bodies.
type Executor interface {
	Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResult, error)
}

// ModelFetcher returns the live model list for a connection by fetching
// "<BaseURL>/models" (or the provider's native equivalent). Returns
// (nil, nil) when the upstream has no model list endpoint or it is
// unreachable; callers fall back to the static registry list.
type ModelFetcher interface {
	Fetch(ctx context.Context, conn *Connection) ([]ModelInfo, error)
}

// Translator converts a request/response between two Formats. The router
// canonicalizes on FormatOpenAI; translators pivot through it.
type Translator interface {
	// Supports reports whether the (from, to) pair is implemented.
	Supports(from, to Format) bool
	// TranslateRequest transforms a JSON request body from -> to, and
	// returns the new body. The upstreamModel is substituted into the
	// translated body. For passthrough (from == to) implementations may
	// return the input unchanged with the model field rewritten.
	TranslateRequest(from, to Format, upstreamModel string, body []byte) ([]byte, error)
	// TranslateResponseJSON transforms a non-streaming JSON response body
	// from upstream format `from` back to client format `to`.
	TranslateResponseJSON(from, to Format, body []byte) ([]byte, error)
	// TranslateResponseStream wraps an SSE stream from upstream format
	// `from` into SSE in client format `to`. It must pipe through with
	// minimal buffering. Returns a reader yielding SSE in client format.
	TranslateResponseStream(ctx context.Context, from, to Format, r io.ReadCloser) (io.ReadCloser, error)
}

// UpstreamError is parsed from a failed upstream response so the router can
// decide whether to fall back to the next model/account.
type UpstreamError struct {
	Status     int
	Message    string
	RetryAfter time.Duration // 0 if unknown
}

// RequestCompressor transforms a request body to reduce token count
// (e.g. RTK tool_result compression). Implementations must be safe for
// concurrent use. A nil compressor disables compression — callers should
// check for nil before calling.
type RequestCompressor interface {
	// Compress rewrites the JSON request body in-place to reduce token count.
	// Returns the (possibly modified) body. Must be fail-open: on any error
	// the original body is returned unchanged.
	Compress(body []byte) []byte
}

// CachedResponse is a response stored in the response cache. For non-stream
// responses Body holds the full JSON payload (in client format). For stream
// responses, StreamChunks holds the concatenation of SSE chunks (each chunk
// prefixed with its SSE framing) ready to be replayed verbatim.
type CachedResponse struct {
	StatusCode    int
	Headers       http.Header
	Body          []byte // non-stream: full JSON response (client format)
	StreamChunks  []byte // stream: concatenated SSE bytes ready to replay
	Stream        bool
	CreatedAt     time.Time
}

// ResponseCache stores and retrieves cached responses keyed by a deterministic
// hash of the request. Implementations must be safe for concurrent use. A nil
// response (or a nil interface) disables caching entirely — callers should
// check for nil before calling.
type ResponseCache interface {
	// Get returns the cached response for the given key, or nil if absent
	// or expired. A miss (including expired entries) returns (nil, false).
	Get(ctx context.Context, key string) (*CachedResponse, bool)
	// Put stores the response under the given key with the configured TTL.
	Put(ctx context.Context, key string, resp *CachedResponse)
	// Delete removes a single entry by key.
	Delete(ctx context.Context, key string)
	// Flush removes all entries.
	Flush(ctx context.Context)
	// Stats returns current cache statistics (entries, hits, misses).
	Stats() CacheStats
	// Close stops background goroutines (sweeper). Safe to call multiple times.
	Close()
}

// CacheStats holds basic cache metrics for observability.
type CacheStats struct {
	Entries int   `json:"entries"`
	Hits    int64 `json:"hits"`
	Misses  int64 `json:"misses"`
}