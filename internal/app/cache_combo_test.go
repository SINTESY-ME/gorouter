package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/responsecache"
)

// cacheComboService builds a RouterService with a 2-model combo ("mycombo":
// openai/gpt-4 + anthropic/claude-3) and the given strategy. Both providers
// have one active connection. The cache is enabled with the given TTL.
func cacheComboService(exec *mockExecutor, strategy string, comboMeta map[string]domain.ComboModelMeta) *RouterService {
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:        "cb1",
				Name:      "mycombo",
				Models:    []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy:  strategy,
				ModelMeta: comboMeta,
			},
		},
	}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{
			{ID: "c-openai", ProviderID: "openai", Name: "k1", IsActive: true},
			{ID: "c-anthropic", ProviderID: "anthropic", Name: "k1", IsActive: true},
		},
	}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})
	mem := responsecache.NewMemory(100, 5*time.Minute, time.Minute)
	srv.Cache = NewCacheService(mem)
	return srv
}

func cacheComboServiceWithConns(exec *mockExecutor, strategy string, connRepo *mockConnectionRepo, comboMeta map[string]domain.ComboModelMeta) *RouterService {
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {
				ID:        "cb1",
				Name:      "mycombo",
				Models:    []string{"openai/gpt-4", "anthropic/claude-3"},
				Strategy:  strategy,
				ModelMeta: comboMeta,
			},
		},
	}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})
	mem := responsecache.NewMemory(100, 5*time.Minute, time.Minute)
	srv.Cache = NewCacheService(mem)
	return srv
}

const comboChatBody = `{"model":"mycombo","messages":[{"role":"user","content":"hi"}]}`
const comboChatBodyDirect = `{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`

// TestRouteCombo_CacheHit_PrewarmedModel verifies that a combo request serves
// from cache when the cache for one of its models was pre-warmed — the cache
// is keyed by the real model, not the combo name.
func TestRouteCombo_CacheHit_PrewarmedModel(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := cacheComboService(exec, StrategyOrderedFallback, nil)

	body := []byte(comboChatBody)
	// Pre-warm the cache for openai/gpt-4 (the first model in the combo).
	key := srv.Cache.ComputeKey(body, "openai/gpt-4", domain.FormatOpenAI)
	srv.Cache.Store(context.Background(), key, http.StatusOK, http.Header{}, []byte(`{"id":"cached","choices":[{"message":{"content":"from-cache"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))

	res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if !res.Cached {
		t.Fatal("expected response to be served from cache")
	}
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("expected no upstream calls, got %v", got)
	}
	if !bytes.Contains(buf, []byte("from-cache")) {
		t.Fatalf("expected cached body, got %s", buf)
	}
}

// TestRouteCombo_CacheStore_KeyedByRealModel verifies that after a combo
// request succeeds via a model, the cache entry is stored under the real
// model's key — not the combo name. A subsequent direct call to that model
// with the same body should hit the cache.
func TestRouteCombo_CacheStore_KeyedByRealModel(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := cacheComboService(exec, StrategyOrderedFallback, nil)

	// First request: combo routes to openai/gpt-4, stores cache under that model.
	body := []byte(comboChatBody)
	res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	time.Sleep(50 * time.Millisecond) // allow async store to complete

	// Second request: call the model directly with the same body.
	// The body has "model":"mycombo" — we pass modelStr="openai/gpt-4" directly.
	directBody := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res2, err := srv.RouteChat(context.Background(), directBody, "openai/gpt-4", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()

	if !res2.Cached {
		t.Fatal("direct model call should hit cache stored by the combo")
	}
}

// TestRouteCombo_CacheHit_OrderedFallback_OnlyCurrentModel verifies that
// ordered_fallback only checks the cache for the model being tried. If the
// cache for model 2 is warm but model 1 is not, the combo should call model 1
// (miss) and only check model 2's cache after model 1 fails.
func TestRouteCombo_CacheHit_OrderedFallback_OnlyCurrentModel(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		failModels: map[string]int{
			"gpt-4": http.StatusServiceUnavailable, // model 1 fails
		},
	}
	srv := cacheComboService(exec, StrategyOrderedFallback, nil)

	body := []byte(comboChatBody)
	// Pre-warm cache for model 2 (anthropic/claude-3) only.
	key := srv.Cache.ComputeKey(body, "anthropic/claude-3", domain.FormatOpenAI)
	srv.Cache.Store(context.Background(), key, http.StatusOK, http.Header{}, []byte(`{"id":"cached","choices":[{"message":{"content":"from-cache"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))

	res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if !res.Cached {
		t.Fatal("expected cache hit from model 2 after model 1 failed")
	}
	// Model 1 should have been called (and failed) before checking model 2's cache.
	// executeOneWithRetry retries up to maxTransientRetries=2, so gpt-4 is called
	// 3 times (attempts 0, 1, 2) before the combo falls back to model 2's cache.
	if got := calledSnapshot(exec); len(got) != 3 {
		t.Fatalf("expected 3 calls to gpt-4 (retries) before cache hit on claude-3, got %d: %v", len(got), got)
	}
	for _, m := range calledSnapshot(exec) {
		if m != "gpt-4" {
			t.Fatalf("expected only gpt-4 calls, got %v", calledSnapshot(exec))
		}
	}
	if !bytes.Contains(buf, []byte("from-cache")) {
		t.Fatalf("expected cached body, got %s", buf)
	}
}

// TestRouteCombo_CacheHit_RoundRobin_AllModels verifies that round-robin
// checks the cache of all models in the combo, returning the first hit.
func TestRouteCombo_CacheHit_RoundRobin_AllModels(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := cacheComboService(exec, StrategyRoundRobin, nil)

	body := []byte(comboChatBody)
	// Pre-warm cache for model 2 only (anthropic/claude-3).
	key := srv.Cache.ComputeKey(body, "anthropic/claude-3", domain.FormatOpenAI)
	srv.Cache.Store(context.Background(), key, http.StatusOK, http.Header{}, []byte(`{"id":"cached","choices":[{"message":{"content":"from-cache"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))

	res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if !res.Cached {
		t.Fatal("expected cache hit from any model in round-robin")
	}
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("expected no upstream calls, got %v", got)
	}
	if !bytes.Contains(buf, []byte("from-cache")) {
		t.Fatalf("expected cached body, got %s", buf)
	}
}

// TestRouteCombo_CacheHit_UnhealthyModel verifies that a cache hit is served
// even when the model is unhealthy — the cache is independent of health.
func TestRouteCombo_CacheHit_UnhealthyModel(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := cacheComboService(exec, StrategyOrderedFallback, nil)

	body := []byte(comboChatBody)
	// Pre-warm cache for model 1 (openai/gpt-4).
	key := srv.Cache.ComputeKey(body, "openai/gpt-4", domain.FormatOpenAI)
	srv.Cache.Store(context.Background(), key, http.StatusOK, http.Header{}, []byte(`{"id":"cached","choices":[{"message":{"content":"from-cache"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))

	// Mark the openai connection as unhealthy.
	srv.Health.MarkUnhealthy("openai/gpt-4", "c-openai")

	res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if !res.Cached {
		t.Fatal("expected cache hit even with unhealthy model")
	}
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("expected no upstream calls for unhealthy model with cache hit, got %v", got)
	}
	if !bytes.Contains(buf, []byte("from-cache")) {
		t.Fatalf("expected cached body, got %s", buf)
	}
}

// TestRouteCombo_CacheHit_Weighted_SameWeight verifies that the weighted
// strategy checks caches of models with the same weight as the chosen model.
func TestRouteCombo_CacheHit_Weighted_SameWeight(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	meta := map[string]domain.ComboModelMeta{
		"openai/gpt-4":       {Weight: 1},
		"anthropic/claude-3": {Weight: 1},
	}
	srv := cacheComboService(exec, StrategyWeighted, meta)

	body := []byte(comboChatBody)
	// Pre-warm cache for anthropic/claude-3 (same weight=1 as gpt-4).
	key := srv.Cache.ComputeKey(body, "anthropic/claude-3", domain.FormatOpenAI)
	srv.Cache.Store(context.Background(), key, http.StatusOK, http.Header{}, []byte(`{"id":"cached","choices":[{"message":{"content":"from-cache"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))

	res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	buf, _ := io.ReadAll(res.Body)
	res.Body.Close()

	if !res.Cached {
		t.Fatal("expected cache hit from same-weight model in weighted strategy")
	}
	if got := calledSnapshot(exec); len(got) != 0 {
		t.Fatalf("expected no upstream calls, got %v", got)
	}
	if !bytes.Contains(buf, []byte("from-cache")) {
		t.Fatalf("expected cached body, got %s", buf)
	}
}

// TestRouteCombo_CacheMiss_Weighted_DifferentWeight verifies that the
// weighted strategy does NOT check caches of models with different weights.
func TestRouteCombo_CacheMiss_Weighted_DifferentWeight(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	meta := map[string]domain.ComboModelMeta{
		"openai/gpt-4":       {Weight: 1},
		"anthropic/claude-3": {Weight: 2}, // different weight
	}
	srv := cacheComboService(exec, StrategyWeighted, meta)

	body := []byte(comboChatBody)
	// Pre-warm cache for anthropic/claude-3 (weight=2).
	// If gpt-4 (weight=1) is chosen first, its cache candidates should NOT
	// include claude-3, so this should be a miss and the upstream should be called.
	key := srv.Cache.ComputeKey(body, "anthropic/claude-3", domain.FormatOpenAI)
	srv.Cache.Store(context.Background(), key, http.StatusOK, http.Header{}, []byte(`{"id":"cached","choices":[{"message":{"content":"from-cache"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))

	// Run multiple times to increase the chance gpt-4 (weight=1) is chosen.
	// With weight 1 vs 2, gpt-4 is chosen ~33% of the time. Run 10 times.
	hitCount := 0
	for i := 0; i < 10; i++ {
		exec.calls = 0
		exec.called = nil
		res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
		if err != nil {
			t.Fatal(err)
		}
		if res.Cached {
			hitCount++
		}
		res.Body.Close()
	}

	// Some requests may pick claude-3 (weight=2) as primary and hit its cache.
	// But when gpt-4 is primary, the cache for claude-3 should NOT be checked.
	// So we expect a mix: at least one upstream call (when gpt-4 is primary)
	// and at least one cache hit (when claude-3 is primary).
	if hitCount == 10 {
		t.Fatal("expected at least one upstream call when gpt-4 (weight=1) is chosen — claude-3 cache should not be checked")
	}
	if hitCount == 0 {
		t.Fatal("expected at least one cache hit when claude-3 (weight=2) is chosen as primary")
	}
}

// TestRouteCombo_CacheHit_CrossCombo verifies that a cache entry stored by
// one combo can be reused by a different combo that shares the same model.
func TestRouteCombo_CacheHit_CrossCombo(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	// Two combos sharing openai/gpt-4.
	comboRepo := &mockComboRepo{
		combos: map[string]*domain.Combo{
			"cb1": {ID: "cb1", Name: "comboA", Models: []string{"openai/gpt-4"}, Strategy: StrategyOrderedFallback},
			"cb2": {ID: "cb2", Name: "comboB", Models: []string{"openai/gpt-4", "anthropic/claude-3"}, Strategy: StrategyOrderedFallback},
		},
	}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{
			{ID: "c-openai", ProviderID: "openai", Name: "k1", IsActive: true},
			{ID: "c-anthropic", ProviderID: "anthropic", Name: "k1", IsActive: true},
		},
	}
	srv := NewRouterService(comboRepo, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})
	mem := responsecache.NewMemory(100, 5*time.Minute, time.Minute)
	srv.Cache = NewCacheService(mem)

	// First request: comboA routes to openai/gpt-4, stores cache.
	bodyA := []byte(`{"model":"comboA","messages":[{"role":"user","content":"hi"}]}`)
	res, err := srv.RouteChat(context.Background(), bodyA, "comboA", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	time.Sleep(50 * time.Millisecond)

	// Second request: comboB with the same body content should hit the cache
	// for openai/gpt-4 (shared model).
	bodyB := []byte(`{"model":"comboB","messages":[{"role":"user","content":"hi"}]}`)
	res2, err := srv.RouteChat(context.Background(), bodyB, "comboB", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()

	if !res2.Cached {
		t.Fatal("comboB should hit cache stored by comboA (shared model openai/gpt-4)")
	}
}

// TestRouteCombo_CacheHit_DirectModel verifies that a cache entry stored by
// a combo can be reused by a direct call to the model.
func TestRouteCombo_CacheHit_DirectModel(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := cacheComboService(exec, StrategyOrderedFallback, nil)

	// First request: combo routes to openai/gpt-4, stores cache.
	body := []byte(comboChatBody)
	res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	time.Sleep(50 * time.Millisecond)

	// Second request: direct call to openai/gpt-4 with same body content.
	directBody := []byte(`{"model":"openai/gpt-4","messages":[{"role":"user","content":"hi"}]}`)
	res2, err := srv.RouteChat(context.Background(), directBody, "openai/gpt-4", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()

	if !res2.Cached {
		t.Fatal("direct model call should hit cache stored by combo")
	}
}

// TestRouteCombo_CacheMiss_DifferentBody verifies that different request
// bodies do not produce cache hits.
func TestRouteCombo_CacheMiss_DifferentBody(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := cacheComboService(exec, StrategyOrderedFallback, nil)

	// First request: combo routes to openai/gpt-4, stores cache.
	body1 := []byte(`{"model":"mycombo","messages":[{"role":"user","content":"hello"}]}`)
	res, err := srv.RouteChat(context.Background(), body1, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	time.Sleep(50 * time.Millisecond)

	// Second request: different body content — should be a cache miss.
	body2 := []byte(`{"model":"mycombo","messages":[{"role":"user","content":"different"}]}`)
	res2, err := srv.RouteChat(context.Background(), body2, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()

	if res2.Cached {
		t.Fatal("different body should not hit cache")
	}
	if got := calledSnapshot(exec); len(got) != 2 {
		t.Fatalf("expected 2 upstream calls (one per request), got %d: %v", len(got), got)
	}
}

// TestRouteCombo_CacheDisabled_NoLookup verifies that when the cache is
// disabled, no cache lookup or store happens.
func TestRouteCombo_CacheDisabled_NoLookup(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
	}
	srv := cacheComboService(exec, StrategyOrderedFallback, nil)
	srv.Cache = nil // disable cache

	body := []byte(comboChatBody)
	res, err := srv.RouteChat(context.Background(), body, "mycombo", false, "key1", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	if res.Cached {
		t.Fatal("response should not be cached when cache is disabled")
	}
	if got := calledSnapshot(exec); len(got) != 1 {
		t.Fatalf("expected 1 upstream call, got %d: %v", len(got), got)
	}
}
