package responsecache

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

func TestMemoryCachePutGet(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()
	ctx := context.Background()
	resp := &domain.CachedResponse{
		StatusCode: 200,
		Body:       []byte(`{"ok":true}`),
		Stream:     false,
	}
	c.Put(ctx, "key1", resp)
	got, ok := c.Get(ctx, "key1")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(got.Body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", got.Body)
	}
}

func TestMemoryCacheMiss(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()
	_, ok := c.Get(context.Background(), "nonexistent")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestMemoryCacheTTLExpiry(t *testing.T) {
	c := NewMemory(100, 50*time.Millisecond, time.Minute)
	defer c.Close()
	ctx := context.Background()
	c.Put(ctx, "key1", &domain.CachedResponse{Body: []byte("x")})
	time.Sleep(60 * time.Millisecond)
	_, ok := c.Get(ctx, "key1")
	if ok {
		t.Fatal("expected expiry miss")
	}
}

func TestMemoryCacheLRUEviction(t *testing.T) {
	c := NewMemory(2, time.Minute, time.Minute)
	defer c.Close()
	ctx := context.Background()
	c.Put(ctx, "a", &domain.CachedResponse{Body: []byte("a")})
	c.Put(ctx, "b", &domain.CachedResponse{Body: []byte("b")})
	// Access "a" to make it more recent than "b"
	c.Get(ctx, "a")
	// Insert "c" — should evict "b" (LRU)
	c.Put(ctx, "c", &domain.CachedResponse{Body: []byte("c")})
	if _, ok := c.Get(ctx, "b"); ok {
		t.Fatal("expected b to be evicted")
	}
	if _, ok := c.Get(ctx, "a"); !ok {
		t.Fatal("expected a to survive")
	}
	if _, ok := c.Get(ctx, "c"); !ok {
		t.Fatal("expected c to survive")
	}
}

func TestMemoryCacheFlush(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()
	ctx := context.Background()
	c.Put(ctx, "a", &domain.CachedResponse{Body: []byte("a")})
	c.Put(ctx, "b", &domain.CachedResponse{Body: []byte("b")})
	c.Flush(ctx)
	if _, ok := c.Get(ctx, "a"); ok {
		t.Fatal("expected flush to remove a")
	}
	if _, ok := c.Get(ctx, "b"); ok {
		t.Fatal("expected flush to remove b")
	}
}

func TestMemoryCacheStats(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()
	ctx := context.Background()
	c.Put(ctx, "a", &domain.CachedResponse{Body: []byte("a")})
	c.Get(ctx, "a") // hit
	c.Get(ctx, "missing") // miss
	stats := c.Stats()
	if stats.Entries != 1 {
		t.Fatalf("expected 1 entry, got %d", stats.Entries)
	}
	if stats.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", stats.Misses)
	}
}

func TestMemoryCacheSweep(t *testing.T) {
	c := NewMemory(100, 50*time.Millisecond, 30*time.Millisecond)
	defer c.Close()
	ctx := context.Background()
	c.Put(ctx, "a", &domain.CachedResponse{Body: []byte("a")})
	time.Sleep(100 * time.Millisecond) // wait for sweep + expiry
	if s := c.Stats(); s.Entries != 0 {
		t.Fatalf("expected 0 entries after sweep, got %d", s.Entries)
	}
}

func TestMemoryCacheUpdate(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()
	ctx := context.Background()
	c.Put(ctx, "a", &domain.CachedResponse{Body: []byte("old")})
	c.Put(ctx, "a", &domain.CachedResponse{Body: []byte("new")})
	got, ok := c.Get(ctx, "a")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(got.Body) != "new" {
		t.Fatalf("expected new body, got %s", got.Body)
	}
	if s := c.Stats(); s.Entries != 1 {
		t.Fatalf("expected 1 entry, got %d", s.Entries)
	}
}

func TestMemoryCacheStreamChunks(t *testing.T) {
	c := NewMemory(100, time.Minute, time.Minute)
	defer c.Close()
	ctx := context.Background()
	c.Put(ctx, "stream1", &domain.CachedResponse{
		Stream:       true,
		StreamChunks: []byte("data: chunk1\ndata: chunk2\n"),
		Headers:      http.Header{"Content-Type": []string{"text/event-stream"}},
	})
	got, ok := c.Get(ctx, "stream1")
	if !ok {
		t.Fatal("expected hit")
	}
	if !got.Stream {
		t.Fatal("expected Stream=true")
	}
	if string(got.StreamChunks) != "data: chunk1\ndata: chunk2\n" {
		t.Fatalf("unexpected chunks: %s", got.StreamChunks)
	}
}