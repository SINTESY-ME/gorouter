package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/responsecache"
)

// TestRouteChat_EmptyStreamNotCached verifies that a 200 response with an
// empty SSE stream (zero content deltas — the Ollama Cloud failure mode that
// left Sintesys stuck in generate_sintesy) is passed through to the client but
// NOT stored in the deterministic response cache. A cached empty answer would
// replay to every identical retry and defeat the client's retry policy.
func TestRouteChat_EmptyStreamNotCached(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		// SSE stream that only carries [DONE] — no content deltas at all.
		body: "data: [DONE]\n\n",
	}
	srv := singleConnService(exec)

	mem := responsecache.NewMemory(10, time.Minute, time.Minute)
	defer mem.Close()
	srv.Cache = NewCacheService(mem)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	for i := 0; i < 2; i++ {
		res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), true, "", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if got := calledSnapshot(exec); len(got) != 2 {
		t.Fatalf("empty stream must not be cached: both requests should reach upstream; upstream calls = %v", got)
	}
}

// TestRouteChat_ContentStreamCached verifies the flip side: a 200 SSE stream
// with real content deltas IS cached, so an identical second request is served
// from cache.
func TestRouteChat_ContentStreamCached(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := singleConnService(exec)

	mem := responsecache.NewMemory(10, time.Minute, time.Minute)
	defer mem.Close()
	srv.Cache = NewCacheService(mem)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	for i := 0; i < 2; i++ {
		res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), true, "", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if got := calledSnapshot(exec); len(got) != 1 {
		t.Fatalf("content stream should be cached on first response; upstream calls = %v", got)
	}
}

// TestRouteChat_EmptyJSONNotCached covers the non-streaming variant: a 200
// JSON completion with empty message content and no tool calls is not cached.
func TestRouteChat_EmptyJSONNotCached(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":""}}]}`,
	}
	srv := singleConnService(exec)
	mem := responsecache.NewMemory(10, time.Minute, time.Minute)
	defer mem.Close()
	srv.Cache = NewCacheService(mem)

	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	for i := 0; i < 2; i++ {
		res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if got := calledSnapshot(exec); len(got) != 2 {
		t.Fatalf("empty JSON completion must not be cached; upstream calls = %v", got)
	}
}

// rawStreamExecutor returns a fixed SSE body verbatim. The shared mockExecutor
// rewrites every streamed body into a single delta.content chunk, so it cannot
// express the stream shapes this file is about (reasoning-only turns).
type rawStreamExecutor struct {
	mu    sync.Mutex
	body  string
	calls int
}

func (e *rawStreamExecutor) Execute(ctx context.Context, req domain.ExecuteRequest) (*domain.ExecuteResult, error) {
	e.mu.Lock()
	if !IsProbeCall(ctx) {
		e.calls++
	}
	e.mu.Unlock()
	return &domain.ExecuteResult{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(e.body)),
		Stream:     true,
	}, nil
}

func (e *rawStreamExecutor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// reasoningOnlyStream is the production failure mode of 2026-09-21: a thinking
// model spends its whole turn on reasoning_content, emits no answer and no
// usage, and closes the stream normally.
const reasoningOnlyStream = `data: {"id":"1","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Weighing the options "},"finish_reason":null}]}

data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_content":"one by one."},"finish_reason":null}]}

data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`

// reasoningThenContentStream carries the same reasoning deltas but ends with a
// real answer: that turn IS cacheable.
const reasoningThenContentStream = reasoningOnlyStream +
	`data: {"id":"1","choices":[{"index":0,"delta":{"content":"Resposta final."},"finish_reason":null}]}

`

func reasoningStreamService(t *testing.T, body string) (*RouterService, *rawStreamExecutor) {
	t.Helper()
	exec := &rawStreamExecutor{body: body}
	connRepo := &mockConnectionRepo{conns: []domain.Connection{
		{ID: "c1", ProviderID: "openai", Name: "test", IsActive: true},
	}}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})
	mem := responsecache.NewMemory(10, time.Minute, time.Minute)
	t.Cleanup(mem.Close)
	srv.Cache = NewCacheService(mem)
	return srv, exec
}

// TestRouteChat_ReasoningOnlyStreamNotCached is the regression for the
// summary_generate incident: the upstream answered 200 with a stream that only
// carried reasoning_content. The guard counted those deltas as content, cached
// the empty answer under the request body's key, and every identical retry was
// then served from cache with latency_ms=0 / pt=ct=0 — structurally unable to
// reach the model again.
func TestRouteChat_ReasoningOnlyStreamNotCached(t *testing.T) {
	srv, exec := reasoningStreamService(t, reasoningOnlyStream)
	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	for i := 0; i < 2; i++ {
		res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), true, "", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if got := exec.callCount(); got != 2 {
		t.Fatalf("reasoning-only stream must not be cached: the retry has to reach upstream; upstream calls = %d", got)
	}
}

// TestRouteChat_ReasoningPlusContentStreamCached is the flip side: reasoning
// deltas are only ignored when the turn produces nothing else. A turn that
// thinks AND answers stays cacheable.
func TestRouteChat_ReasoningPlusContentStreamCached(t *testing.T) {
	srv, exec := reasoningStreamService(t, reasoningThenContentStream)
	body := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	for i := 0; i < 2; i++ {
		res, err := srv.RouteChat(context.Background(), body, extractModelMust(body), true, "", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if got := exec.callCount(); got != 1 {
		t.Fatalf("a stream carrying an answer must stay cacheable; upstream calls = %d", got)
	}
}

// TestResponseHasNoContent pins the rule the cache guard follows, delta by
// delta: reasoning is not an answer.
func TestResponseHasNoContent(t *testing.T) {
	cases := []struct {
		name     string
		buf      string
		isStream bool
		want     bool // true = no usable content (never cache)
	}{
		{
			name:     "reasoning-only stream",
			buf:      reasoningOnlyStream,
			isStream: true,
			want:     true,
		},
		{
			name:     "empty SSE stream",
			buf:      "data: [DONE]\n\n",
			isStream: true,
			want:     true,
		},
		{
			name:     "usage-only chunk before the answer",
			buf:      `data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":0}}` + "\n\n",
			isStream: true,
			want:     true,
		},
		{
			name:     "stream with content",
			buf:      `data: {"choices":[{"delta":{"content":"oi"}}]}` + "\n\n",
			isStream: true,
			want:     false,
		},
		{
			name:     "reasoning plus content",
			buf:      reasoningThenContentStream,
			isStream: true,
			want:     false,
		},
		{
			name:     "tool call delta",
			buf:      `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1"}]}}]}` + "\n\n",
			isStream: true,
			want:     false,
		},
		{
			name:     "whitespace-only content",
			buf:      `data: {"choices":[{"delta":{"content":"   "}}]}` + "\n\n",
			isStream: true,
			want:     true,
		},
		{
			name:     "empty buffer",
			buf:      "",
			isStream: true,
			want:     true,
		},
		{
			name:     "reasoning-only JSON completion",
			buf:      `{"choices":[{"message":{"content":"","reasoning_content":"thinking"}}]}`,
			isStream: false,
			want:     true,
		},
		{
			name:     "JSON completion with content",
			buf:      `{"choices":[{"message":{"content":"resposta"}}]}`,
			isStream: false,
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := responseHasNoContent([]byte(tc.buf), tc.isStream); got != tc.want {
				t.Fatalf("responseHasNoContent = %v, want %v", got, tc.want)
			}
		})
	}
}

// semanticStoreFor runs one streamed request through a service whose semantic
// cache is the only cache enabled, and returns how many entries it holds.
func semanticStoreFor(t *testing.T, body string) int {
	t.Helper()
	exec := &rawStreamExecutor{body: body}
	connRepo := &mockConnectionRepo{conns: []domain.Connection{
		{ID: "c1", ProviderID: "openai", Name: "test", IsActive: true},
	}}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})
	mem := newMockSemanticCache()
	svc := NewSemanticCacheService(mem, mockEmbeddingProvider{}, 0.9, SemanticModeActive)
	svc.SetEnabled(true)
	srv.SemanticCache = svc

	reqBody := []byte(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	res, err := srv.RouteChat(context.Background(), reqBody, "openai/gpt-4o", true, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if exec.callCount() != 1 {
		t.Fatalf("expected exactly one upstream call, got %d", exec.callCount())
	}
	return len(mem.stored)
}

// TestSemanticCacheSkipsEmptyStream: a similarity match on an empty answer is
// worse than the exact-key replay — it reaches requests that were never
// identical — so the no-content guard applies to the semantic cache too.
func TestSemanticCacheSkipsEmptyStream(t *testing.T) {
	if got := semanticStoreFor(t, reasoningOnlyStream); got != 0 {
		t.Fatalf("reasoning-only stream must not enter the semantic cache; entries = %d", got)
	}
}

// TestSemanticCacheStoresAnsweredStream is the flip side, so the guard above is
// not just "streams are never stored".
func TestSemanticCacheStoresAnsweredStream(t *testing.T) {
	if got := semanticStoreFor(t, reasoningThenContentStream); got != 1 {
		t.Fatalf("a stream carrying an answer must be stored; entries = %d", got)
	}
}
