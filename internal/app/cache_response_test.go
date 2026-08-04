package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/responsecache"
)

func TestComputeKeyDeterministic(t *testing.T) {
	cs := &CacheService{cache: nil}
	body := []byte(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	k1 := cs.ComputeKey(body, "openai/gpt-4o", domain.FormatOpenAI)
	k2 := cs.ComputeKey(body, "openai/gpt-4o", domain.FormatOpenAI)
	if k1 != k2 {
		t.Fatal("same input should produce same key")
	}
}

func TestComputeKeyDifferentModel(t *testing.T) {
	cs := &CacheService{cache: nil}
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	k1 := cs.ComputeKey(body, "openai/gpt-4o", domain.FormatOpenAI)
	k2 := cs.ComputeKey(body, "anthropic/claude-3", domain.FormatOpenAI)
	if k1 == k2 {
		t.Fatal("different models should produce different keys")
	}
}

func TestComputeKeyDifferentFormat(t *testing.T) {
	cs := &CacheService{cache: nil}
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	k1 := cs.ComputeKey(body, "openai/gpt-4o", domain.FormatOpenAI)
	k2 := cs.ComputeKey(body, "openai/gpt-4o", domain.FormatAnthropic)
	if k1 == k2 {
		t.Fatal("different formats should produce different keys")
	}
}

func TestComputeKeyIgnoresEphemeralFields(t *testing.T) {
	cs := &CacheService{cache: nil}
	body1 := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"user":"alice"}`)
	body2 := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"user":"bob"}`)
	k1 := cs.ComputeKey(body1, "m", domain.FormatOpenAI)
	k2 := cs.ComputeKey(body2, "m", domain.FormatOpenAI)
	if k1 != k2 {
		t.Fatal("ephemeral field 'user' should not affect key")
	}
}

func TestComputeKeyDifferentContent(t *testing.T) {
	cs := &CacheService{cache: nil}
	body1 := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`)
	body2 := []byte(`{"model":"m","messages":[{"role":"user","content":"world"}]}`)
	k1 := cs.ComputeKey(body1, "m", domain.FormatOpenAI)
	k2 := cs.ComputeKey(body2, "m", domain.FormatOpenAI)
	if k1 == k2 {
		t.Fatal("different content should produce different keys")
	}
}

func TestComputeKeyFieldOrderInvariant(t *testing.T) {
	cs := &CacheService{cache: nil}
	body1 := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"temperature":0.7}`)
	body2 := []byte(`{"temperature":0.7,"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	k1 := cs.ComputeKey(body1, "m", domain.FormatOpenAI)
	k2 := cs.ComputeKey(body2, "m", domain.FormatOpenAI)
	if k1 != k2 {
		t.Fatal("field order should not affect key")
	}
}

func TestCacheServiceEnabled(t *testing.T) {
	if (&CacheService{cache: nil}).Enabled() {
		t.Fatal("nil cache should not be enabled")
	}
}

func TestComputeKeyToolsOrderInvariant(t *testing.T) {
	cs := &CacheService{cache: nil}
	body1 := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"b"}},{"type":"function","function":{"name":"a"}}]}`)
	body2 := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"a"}},{"type":"function","function":{"name":"b"}}]}`)
	if cs.ComputeKey(body1, "m", domain.FormatOpenAI) != cs.ComputeKey(body2, "m", domain.FormatOpenAI) {
		t.Fatal("tools order (a set) should not affect the key")
	}
}

func TestComputeKeyN(t *testing.T) {
	cs := &CacheService{cache: nil}
	base := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	n1 := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"n":1}`)
	n2 := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"n":2}`)
	if cs.ComputeKey(base, "m", domain.FormatOpenAI) != cs.ComputeKey(n1, "m", domain.FormatOpenAI) {
		t.Fatal("n:1 (the default) should not affect the key")
	}
	if cs.ComputeKey(n1, "m", domain.FormatOpenAI) == cs.ComputeKey(n2, "m", domain.FormatOpenAI) {
		t.Fatal("n:2 changes the response shape and must affect the key")
	}
}

func TestMessageCount(t *testing.T) {
	if n := messageCount([]byte(`{"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"}]}`)); n != 2 {
		t.Fatalf("messageCount = %d, want 2", n)
	}
	if n := messageCount([]byte(`not json`)); n != -1 {
		t.Fatalf("messageCount(invalid) = %d, want -1", n)
	}
}

func TestNormalizeSemanticText(t *testing.T) {
	if got := normalizeSemanticText("  Hello World  "); got != "hello world" {
		t.Fatalf("normalizeSemanticText = %q, want %q", got, "hello world")
	}
}

func TestCacheServicePerRequestTTL(t *testing.T) {
	mem := responsecache.NewMemory(10, time.Minute, time.Minute)
	defer mem.Close()
	cs := NewCacheService(mem)
	ctx := context.Background()

	// No per-request TTL → entry stays (backend TTL applies).
	key := cs.ComputeKey([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), "m", domain.FormatOpenAI)
	cs.Store(ctx, key, 200, http.Header{}, []byte(`{}`))
	if _, ok := cs.Lookup(ctx, key); !ok {
		t.Fatal("entry should be present without a per-request TTL")
	}

	// Short per-request TTL → entry expires and Lookup returns a miss.
	shortCtx := WithCacheTTL(context.Background(), 30*time.Millisecond)
	key2 := cs.ComputeKey([]byte(`{"messages":[{"role":"user","content":"bye"}]}`), "m", domain.FormatOpenAI)
	cs.Store(shortCtx, key2, 200, http.Header{}, []byte(`{}`))
	time.Sleep(50 * time.Millisecond)
	if _, ok := cs.Lookup(ctx, key2); ok {
		t.Fatal("entry with an expired per-request TTL should miss")
	}
}

func TestComputeKeyCachingGroups(t *testing.T) {
	cs := &CacheService{cache: nil, groups: map[string]string{"gpt-4o": "gpt-family", "gpt-4o-mini": "gpt-family"}}
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	k1 := cs.ComputeKey(body, "openai/gpt-4o", domain.FormatOpenAI)
	k2 := cs.ComputeKey(body, "openai/gpt-4o-mini", domain.FormatOpenAI)
	if k1 != k2 {
		t.Fatal("models in the same caching group should share the key")
	}
	k3 := cs.ComputeKey(body, "anthropic/claude-3", domain.FormatOpenAI)
	if k1 == k3 {
		t.Fatal("a model outside the group must not share the key")
	}
}

func TestResolveGroup(t *testing.T) {
	cs := &CacheService{cache: nil, groups: map[string]string{"gpt-4o": "g"}}
	if got := cs.resolveGroup("openai/gpt-4o"); got != "group:g" {
		t.Fatalf("resolveGroup(provider/model) = %q, want group:g", got)
	}
	if got := cs.resolveGroup("gpt-4o"); got != "group:g" {
		t.Fatalf("resolveGroup(bare) = %q, want group:g", got)
	}
	if got := cs.resolveGroup("openai/gpt-4o-mini"); got != "openai/gpt-4o-mini" {
		t.Fatalf("resolveGroup(non-group) = %q", got)
	}
}
