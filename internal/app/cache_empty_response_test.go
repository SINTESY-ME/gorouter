package app

import (
	"context"
	"io"
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
