package app

import (
	"sync"
	"time"
)

// RateLimiter is an in-memory per-key token bucket limiter. A key with
// rpm=0 is unlimited. Buckets are created lazily on first request and
// refilled continuously at rpm/60 tokens per second, capped at rpm.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	tokens   float64
	last     time.Time
	rpm      int
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: make(map[string]*tokenBucket)}
}

// Allow reports whether a request from the given key is within its rate
// limit. rpm=0 means unlimited (always allowed).
func (rl *RateLimiter) Allow(key string, rpm int) bool {
	if rpm <= 0 {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok || b.rpm != rpm {
		// First request or RPM changed: start with full bucket.
		b = &tokenBucket{tokens: float64(rpm), last: time.Now(), rpm: rpm}
		rl.buckets[key] = b
	}

	// Refill: tokens accrue at rpm/60 per second since last request.
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.tokens = min(float64(rpm), b.tokens+elapsed*float64(rpm)/60.0)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}