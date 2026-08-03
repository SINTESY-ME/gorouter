package app

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// mockSemanticCache implements domain.SemanticCache for testing.
type mockSemanticCache struct {
	stored     map[string]*domain.CachedResponse
	embeddings map[string][]float32
}

func newMockSemanticCache() *mockSemanticCache {
	return &mockSemanticCache{stored: map[string]*domain.CachedResponse{}, embeddings: map[string][]float32{}}
}

func (m *mockSemanticCache) Get(ctx context.Context, key string, embedding []float32, threshold float64) (*domain.CachedResponse, float64, bool) {
	if resp, ok := m.stored[key]; ok {
		return resp, 1.0, true
	}
	return nil, 0, false
}
func (m *mockSemanticCache) Put(ctx context.Context, key string, embedding []float32, resp *domain.CachedResponse) {
	m.stored[key] = resp
}
func (m *mockSemanticCache) Flush(ctx context.Context) {
	m.stored = map[string]*domain.CachedResponse{}
}
func (m *mockSemanticCache) Stats() domain.CacheStats {
	return domain.CacheStats{Entries: len(m.stored)}
}
func (m *mockSemanticCache) Close() {}

// mockEmbeddingProvider returns a fixed vector.
type mockEmbeddingProvider struct{}

func (mockEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}

func TestRouteChat_SemanticCacheHit(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"from upstream"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
	}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID: "c1", ProviderID: "openai", Name: "test", IsActive: true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})

	// Pre-populate the semantic cache under the direct model key.
	mem := newMockSemanticCache()
	mem.stored["openai/gpt-4o/openai"] = &domain.CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte(`{"id":"cached","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"cached"}}],"usage":{"prompt_tokens":8,"completion_tokens":4}}`),
	}
	svc := NewSemanticCacheService(mem, mockEmbeddingProvider{}, 0.9, SemanticModeActive)
	svc.SetEnabled(true)
	srv.SemanticCache = svc

	body := []byte(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"similar prompt"}]}`)
	res, err := srv.RouteChat(context.Background(), body, "openai/gpt-4o", false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if !res.Cached {
		t.Fatal("expected semantic cache hit (Cached=true)")
	}
	if res.Headers.Get("x-gr-semantic-cache-hit") != "true" {
		t.Fatal("expected x-gr-semantic-cache-hit header")
	}
	buf, _ := io.ReadAll(res.Body)
	if string(buf) != `{"id":"cached","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"cached"}}],"usage":{"prompt_tokens":8,"completion_tokens":4}}` {
		t.Fatalf("expected cached body, got %s", string(buf))
	}
	// The upstream executor must NOT be called.
	if len(exec.called) != 0 {
		t.Fatalf("expected no upstream calls on cache hit, got %v", exec.called)
	}
}

func TestRouteChat_SemanticCacheMiss_RoutesToUpstream(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"from upstream"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
	}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID: "c1", ProviderID: "openai", Name: "test", IsActive: true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})

	// Empty semantic cache -> must miss and hit upstream.
	svc := NewSemanticCacheService(newMockSemanticCache(), mockEmbeddingProvider{}, 0.9, SemanticModeActive)
	svc.SetEnabled(true)
	srv.SemanticCache = svc

	body := []byte(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	res, err := srv.RouteChat(context.Background(), body, "openai/gpt-4o", false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Cached {
		t.Fatal("expected miss, got cached response")
	}
	if len(exec.called) != 1 {
		t.Fatalf("expected 1 upstream call, got %v", exec.called)
	}
}

func TestRouteChat_SemanticCacheLazy_OnlyAfterWarmup(t *testing.T) {
	exec := &mockExecutor{
		status: 200,
		body:   `{"id":"1","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"from upstream"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
	}
	connRepo := &mockConnectionRepo{
		conns: []domain.Connection{{
			ID: "c1", ProviderID: "openai", Name: "test", IsActive: true,
		}},
	}
	srv := NewRouterService(&mockComboRepo{}, connRepo, exec, &mockTranslator{}, &mockUsageRepo{})

	mem := newMockSemanticCache()
	mem.stored["openai/gpt-4o/openai"] = &domain.CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte(`{"id":"cached","choices":[{"message":{"content":"cached"}}]}`),
	}
	svc := NewSemanticCacheService(mem, mockEmbeddingProvider{}, 0.9, SemanticModeLazy)
	svc.SetEnabled(true)
	srv.SemanticCache = svc

	// Lazy mode with < 50 entries: lookup is skipped even though a stored
	// entry exists. Upstream is called.
	body := []byte(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"similar prompt"}]}`)
	res, err := srv.RouteChat(context.Background(), body, "openai/gpt-4o", false, "", RouteOptions{InputFormat: domain.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Cached {
		t.Fatal("lazy mode below warmup should not serve cached response")
	}
	if len(exec.called) != 1 {
		t.Fatalf("expected 1 upstream call, got %v", exec.called)
	}
}
