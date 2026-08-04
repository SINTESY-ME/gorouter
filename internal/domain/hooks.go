package domain

import (
	"context"
	"net/http"
)

// HookContext carries the request through the hook pipeline. Hooks may mutate
// Body and Model to rewrite the request before it is routed or cached. It is
// only allocated when at least one hook is registered, keeping the no-hook hot
// path free of allocations.
type HookContext struct {
	RequestID   string
	Model       string // client-visible model name (may be a combo name)
	Stream      bool
	APIKey      string
	InputFormat Format
	Endpoint    string // "" for chat; "embeddings", "images/generations", ...
	Body        []byte // request body in client format; hooks may replace it
}

// HookResponse describes a completed upstream response for post-call hooks.
// For non-stream responses Body holds the full payload and may be replaced by
// a hook before the response reaches the client. Headers may also be replaced.
type HookResponse struct {
	StatusCode          int
	Headers             http.Header
	Body                []byte
	Stream              bool
	Provider            string
	Model               string
	ConnectionID        string
	PromptTokens        int
	CompletionTokens    int
	CacheReadTokens     int
	CacheCreationTokens int
	Cost                float64
	LatencyMs           int64
	// TTFTMs is the time to first token, set for streaming responses (0 for
	// non-stream and cache hits).
	TTFTMs int64
}

// HookRejectError is returned by a hook to reject a request or transform an
// upstream failure. Status defaults to 400 (mirroring LiteLLM's
// RejectedRequestError) but a hook may set 403, 429, etc.
type HookRejectError struct {
	Status  int
	Message string
}

func (e *HookRejectError) Error() string { return e.Message }

// PreCallHook runs before the response-cache lookup and routing decision. It
// is the admission gate: returning a non-nil error rejects the request.
type PreCallHook interface {
	PreCall(ctx context.Context, hc *HookContext) error
}

// PostCallHook runs after a successful (< 400) non-stream chat response has
// been fully buffered. Hooks may inspect or replace HookResponse.Body and
// HookResponse.Headers. Fail-closed: an error turns the request into a failure.
type PostCallHook interface {
	PostCall(ctx context.Context, hc *HookContext, res *HookResponse) error
}

// PostCallFailureHook runs when routing fails or an upstream error response is
// produced. The returned error, when non-nil, replaces the error sent to the
// client (LiteLLM's post_call_failure_hook returning an HTTPException).
type PostCallFailureHook interface {
	PostCallFailure(ctx context.Context, hc *HookContext, status int, err error) error
}
