package redis

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/responsecache"
)

func TestClientSetGetDel(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := c.Get(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Fatalf("get = %q, %v; want v, nil", got, err)
	}
	if b, _ := c.Get(ctx, "missing"); b != nil {
		t.Fatalf("missing key returned %q", b)
	}
	if ok, _ := c.Exists(ctx, "k"); !ok {
		t.Fatal("k should exist")
	}
	if err := c.Del(ctx, "k"); err != nil {
		t.Fatalf("del: %v", err)
	}
	if b, _ := c.Get(ctx, "k"); b != nil {
		t.Fatal("k should be gone after del")
	}
	if ok, _ := c.Exists(ctx, "k"); ok {
		t.Fatal("k should not exist after del")
	}
}

func TestClientSetNX(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	ok, err := c.SetNX(ctx, "k", []byte("1"), time.Minute)
	if err != nil || !ok {
		t.Fatalf("first SetNX = %v, %v; want true", ok, err)
	}
	ok, err = c.SetNX(ctx, "k", []byte("2"), time.Minute)
	if err != nil || ok {
		t.Fatalf("second SetNX = %v, %v; want false", ok, err)
	}
}

func TestClientScanKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	c.Set(ctx, "cache:a", []byte("1"), time.Minute)
	c.Set(ctx, "cache:b", []byte("1"), time.Minute)
	c.Set(ctx, "other", []byte("1"), time.Minute)

	keys, err := c.ScanKeys(ctx, "cache:*")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("scan keys = %v; want 2 cache:* keys", keys)
	}
}

func TestClientAuth(t *testing.T) {
	mr := miniredis.NewMiniRedis()
	mr.RequireAuth("secret")
	if err := mr.Start(); err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	c, err := New("redis://:secret@" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("ping with auth: %v", err)
	}
}

func TestDualCacheGetPutAndShared(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	mem := responsecache.NewMemory(100, time.Minute, time.Minute)
	defer mem.Close()
	dc := NewDualCache(mem, c, time.Minute)

	resp := &domain.CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"X-Test": {"1"}},
		Body:       []byte(`{"x":1}`),
	}
	dc.Put(ctx, "key1", resp)

	got, ok := dc.Get(ctx, "key1")
	if !ok || got.StatusCode != 200 || string(got.Body) != `{"x":1}` {
		t.Fatalf("get after put failed: ok=%v status=%d body=%s", ok, got.StatusCode, got.Body)
	}

	// A second instance (fresh memory layer) must read the entry from Redis.
	mem2 := responsecache.NewMemory(100, time.Minute, time.Minute)
	defer mem2.Close()
	dc2 := NewDualCache(mem2, c, time.Minute)
	got2, ok := dc2.Get(ctx, "key1")
	if !ok || string(got2.Body) != `{"x":1}` {
		t.Fatalf("entry not shared via redis: ok=%v body=%s", ok, got2.Body)
	}
	if got2.Headers.Get("X-Test") != "1" {
		t.Fatalf("headers did not round-trip: %v", got2.Headers)
	}
}

func TestDualCacheFlush(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	mem := responsecache.NewMemory(100, time.Minute, time.Minute)
	defer mem.Close()
	dc := NewDualCache(mem, c, time.Minute)
	dc.Put(ctx, "k", &domain.CachedResponse{StatusCode: 200, Body: []byte(`{}`)})

	dc.Flush(ctx)

	// Both layers must be empty after flush.
	if _, ok := dc.Get(ctx, "k"); ok {
		t.Fatal("memory still has entry after flush")
	}
	mem2 := responsecache.NewMemory(100, time.Minute, time.Minute)
	defer mem2.Close()
	dc2 := NewDualCache(mem2, c, time.Minute)
	if _, ok := dc2.Get(ctx, "k"); ok {
		t.Fatal("redis still has entry after flush")
	}
}

func TestSharedProbeLockAndResult(t *testing.T) {
	mr := miniredis.RunT(t)
	c, err := New("redis://" + mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sp := NewSharedProbe(c)
	ctx := context.Background()

	if !sp.AcquireLock(ctx, "openai/gpt-4", "c1") {
		t.Fatal("first lock acquire should win")
	}
	if sp.AcquireLock(ctx, "openai/gpt-4", "c1") {
		t.Fatal("second lock acquire should fail while held")
	}
	sp.ReleaseLock(ctx, "openai/gpt-4", "c1")
	if !sp.AcquireLock(ctx, "openai/gpt-4", "c1") {
		t.Fatal("lock acquire after release should win")
	}
	sp.ReleaseLock(ctx, "openai/gpt-4", "c1")

	if _, ok := sp.FreshResult(ctx, "openai/gpt-4", "c1"); ok {
		t.Fatal("no result should exist yet")
	}
	sp.StoreResult(ctx, "openai/gpt-4", "c1", true)
	if healthy, ok := sp.FreshResult(ctx, "openai/gpt-4", "c1"); !ok || !healthy {
		t.Fatal("expected a healthy shared result")
	}
	sp.StoreResult(ctx, "openai/gpt-4", "c1", false)
	if healthy, ok := sp.FreshResult(ctx, "openai/gpt-4", "c1"); !ok || healthy {
		t.Fatal("expected an unhealthy shared result")
	}
}
