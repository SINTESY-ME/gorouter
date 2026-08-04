package redis

import (
	"context"
	"time"
)

// healthLockTTL covers a full probe run (20s timeout) plus margin, so the
// lock is never held past a probe that hangs until its deadline.
const healthLockTTL = 30 * time.Second

// healthResultTTL is how long a shared probe result is considered fresh.
// After it expires, any instance may re-probe.
const healthResultTTL = 60 * time.Second

// SharedProbe coordinates background health probes across gorouter instances
// so that only one instance probes a failing (model, connection) pair at a
// time and every instance shares the result — mirroring LiteLLM's
// SharedHealthCheckManager (Redis lock + cached results). Nil disables sharing
// (each instance probes independently).
type SharedProbe struct {
	r *Client
}

// NewSharedProbe builds a coordinator backed by the given Redis client.
func NewSharedProbe(r *Client) *SharedProbe { return &SharedProbe{r: r} }

func healthKey(parts ...string) string {
	// health:lock:{model}|{conn} and health:result:{model}|{conn}
	s := "health:"
	for i, p := range parts {
		if i > 0 {
			s += "|"
		}
		s += p
	}
	return s
}

func lockKey(modelStr, connID string) string   { return healthKey("lock", modelStr, connID) }
func resultKey(modelStr, connID string) string { return healthKey("result", modelStr, connID) }

// AcquireLock atomically reserves the right to probe. Returns true when this
// instance should run the probe. The caller must ReleaseLock when done.
func (s *SharedProbe) AcquireLock(ctx context.Context, modelStr, connID string) bool {
	ok, err := s.r.SetNX(ctx, lockKey(modelStr, connID), []byte("1"), healthLockTTL)
	return err == nil && ok
}

// ReleaseLock frees the probe lock.
func (s *SharedProbe) ReleaseLock(ctx context.Context, modelStr, connID string) {
	_ = s.r.Del(ctx, lockKey(modelStr, connID))
}

// StoreResult caches the probe outcome so other instances reuse it instead of
// re-probing.
func (s *SharedProbe) StoreResult(ctx context.Context, modelStr, connID string, healthy bool) {
	v := []byte("0")
	if healthy {
		v = []byte("1")
	}
	_ = s.r.Set(ctx, resultKey(modelStr, connID), v, healthResultTTL)
}

// FreshResult returns the shared probe outcome when a fresh one exists.
func (s *SharedProbe) FreshResult(ctx context.Context, modelStr, connID string) (healthy, ok bool) {
	v, err := s.r.Get(ctx, resultKey(modelStr, connID))
	if err != nil || v == nil {
		return false, false
	}
	return string(v) == "1", true
}
