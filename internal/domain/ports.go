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