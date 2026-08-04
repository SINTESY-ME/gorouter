package app

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/redis"
)

// TestHealthProberSharedResultSkipsProbe verifies that with a Shared probe, a
// fresh result from another instance is applied locally without running the
// (expensive) probe call, and that the local in-flight flag is released.
func TestHealthProberSharedResultSkipsProbe(t *testing.T) {
	mr := miniredis.RunT(t)
	rc, err := redis.New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	exec := &mockExecutor{status: 200, body: `{"id":"1","choices":[{"message":{"content":"ok"}}]}`}
	h := NewHealthTracker()
	prober := NewHealthProber(h, &mockConnectionRepo{}, exec, &mockTranslator{}, nil)
	prober.Shared = redis.NewSharedProbe(rc)
	m := domain.ModelID{Provider: "openai", Model: "gpt-4"}

	// Unhealthy pair with a fresh shared "unhealthy" result.
	h.MarkUnhealthy("openai/gpt-4", "c1")
	prober.Shared.StoreResult(context.Background(), "openai/gpt-4", "c1", false)

	prober.RunProbe("openai/gpt-4", m, "c1")
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("probe must not execute when a fresh shared result exists, got %v", got)
	}
	if !h.IsUnhealthy("openai/gpt-4", "c1") {
		t.Fatal("pair should remain unhealthy after shared unhealthy result")
	}
	// The in-flight flag must be released so future requests can re-probe.
	if !h.TryStartProbe("openai/gpt-4", "c1") {
		t.Fatal("probe-in-flight flag not released after applying shared result")
	}
	h.MarkHealthy("openai/gpt-4", "c1")

	// A fresh shared "healthy" result restores the pair without probing.
	prober.Shared.StoreResult(context.Background(), "openai/gpt-4", "c1", true)
	h.MarkUnhealthy("openai/gpt-4", "c1")
	prober.RunProbe("openai/gpt-4", m, "c1")
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("probe must not execute with a healthy shared result, got %v", got)
	}
	if h.IsUnhealthy("openai/gpt-4", "c1") {
		t.Fatal("pair should be healthy after shared healthy result")
	}
}
