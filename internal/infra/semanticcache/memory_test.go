package semanticcache

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

func TestMemory_GetHit(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()

	ctx := context.Background()
	resp := &domain.CachedResponse{Body: []byte(`{"ok":true}`), StatusCode: 200}
	// Query vector pointing in the same direction as stored vector.
	stored := []float32{1, 0, 0}
	query := []float32{0.99, 0.14, 0}
	c.Put(ctx, "model/format", stored, resp)

	got, sim, ok := c.Get(ctx, "model/format", query, 0.9)
	if !ok {
		t.Fatal("expected hit")
	}
	if got != resp {
		t.Fatal("expected cached response")
	}
	if math.Abs(sim-0.9901) > 1e-3 {
		t.Fatalf("expected sim ~0.9901, got %f", sim)
	}
}

func TestMemory_MissBelowThreshold(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()

	ctx := context.Background()
	resp := &domain.CachedResponse{Body: []byte(`{"ok":true}`)}
	c.Put(ctx, "model/format", []float32{1, 0, 0}, resp)

	// Orthogonal vector: similarity ~0, below threshold.
	got, _, ok := c.Get(ctx, "model/format", []float32{0, 1, 0}, 0.5)
	if ok {
		t.Fatal("expected miss for orthogonal query")
	}
	if got != nil {
		t.Fatal("expected nil response")
	}
}

func TestMemory_ScopedByKey(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()

	ctx := context.Background()
	c.Put(ctx, "modelA/format", []float32{1, 0, 0}, &domain.CachedResponse{Body: []byte(`"a"`)})

	// Same embedding but different key must miss.
	_, _, ok := c.Get(ctx, "modelB/format", []float32{1, 0, 0}, 0.9)
	if ok {
		t.Fatal("expected miss for different key")
	}
}

func TestMemory_TTLExpiry(t *testing.T) {
	c := NewMemory(100, 10*time.Millisecond, time.Minute)
	defer c.Close()

	ctx := context.Background()
	c.Put(ctx, "model/format", []float32{1, 0, 0}, &domain.CachedResponse{Body: []byte(`{"ok":true}`)})

	time.Sleep(30 * time.Millisecond)
	_, _, ok := c.Get(ctx, "model/format", []float32{1, 0, 0}, 0.9)
	if ok {
		t.Fatal("expected miss after TTL")
	}
}

func TestMemory_Flush(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()

	ctx := context.Background()
	c.Put(ctx, "model/format", []float32{1, 0, 0}, &domain.CachedResponse{Body: []byte(`{"ok":true}`)})
	c.Flush(ctx)

	stats := c.Stats()
	if stats.Entries != 0 {
		t.Fatalf("expected 0 entries after flush, got %d", stats.Entries)
	}
}

func TestL2Norm(t *testing.T) {
	n := l2Norm([]float32{3, 4})
	if math.Abs(n-5) > 1e-9 {
		t.Fatalf("expected norm 5, got %f", n)
	}
}