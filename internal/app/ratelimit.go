package app

import (
	"sync"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

// RateLimiter enforces in-memory sliding-window request limits per API key.
// Each key can carry multiple rate limits (each with its own window); a
// request is allowed only when every limit has room (AND semantics), so a
// key configured with "5 req/5h" and "100 req/7d" is blocked if either
// window is full.
//
// State is process-local: windows reset on restart. When limits are edited
// the affected windows are dropped and rebuilt.
type RateLimiter struct {
	mu      sync.Mutex
	windows map[string][]rateWindow
}

type rateWindow struct {
	id         string
	max        float64
	duration   time.Duration
	timestamps []time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{windows: make(map[string][]rateWindow)}
}

// Allow reports whether the key is within all of its rate limits. When
// limits is empty the key is unlimited. On success it records the request
// timestamp in each window. On a violation it returns the time until the
// oldest in-window request falls out (so the caller can set Retry-After).
func (rl *RateLimiter) Allow(key string, limits []domain.KeyLimit) (bool, time.Duration) {
	if len(limits) == 0 {
		return true, 0
	}

	// Normalize the active rate limits, skipping invalid or non-rate ones.
	type norm struct {
		max float64
		dur time.Duration
	}
	active := make(map[string]norm, len(limits))
	for _, l := range limits {
		if l.Kind != domain.KeyLimitRate || l.Max <= 0 {
			continue
		}
		dur, err := domain.ParseWindowDuration(l.Duration)
		if err != nil || dur <= 0 {
			continue
		}
		active[l.ID] = norm{max: l.Max, dur: dur}
	}
	if len(active) == 0 {
		return true, 0
	}

	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Reconcile the key's windows to the active limit set: keep + update the
	// survivors (pruning expired timestamps), drop removed ones, and add new
	// ones.
	prev := rl.windows[key]
	live := make([]rateWindow, 0, len(active))
	for i := range prev {
		n, ok := active[prev[i].id]
		if !ok {
			continue
		}
		prev[i].max = n.max
		prev[i].duration = n.dur
		prev[i].prune(now)
		live = append(live, prev[i])
	}
	for id, n := range active {
		if !hasWindow(live, id) {
			live = append(live, rateWindow{id: id, max: n.max, duration: n.dur})
		}
	}
	rl.windows[key] = live

	// First pass: every window must have room. If one is full, report the
	// time until its oldest request falls out of the window.
	var retry time.Duration
	for i := range live {
		w := &live[i]
		if float64(len(w.timestamps)) >= w.max {
			remaining := w.duration - now.Sub(w.timestamps[0])
			if remaining < 0 {
				remaining = 0
			}
			if retry == 0 || remaining > retry {
				retry = remaining
			}
		}
	}
	if retry > 0 {
		return false, retry
	}

	// All windows have room: record the request in each.
	for i := range live {
		live[i].timestamps = append(live[i].timestamps, now)
	}
	rl.windows[key] = live
	return true, 0
}

func hasWindow(ws []rateWindow, id string) bool {
	for i := range ws {
		if ws[i].id == id {
			return true
		}
	}
	return false
}

// prune drops timestamps older than the window.
func (w *rateWindow) prune(now time.Time) {
	cutoff := now.Add(-w.duration)
	kept := w.timestamps[:0]
	for _, ts := range w.timestamps {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	w.timestamps = kept
}
