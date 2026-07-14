package app

import (
	"testing"

	"github.com/jhon/gorouter/internal/domain"
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