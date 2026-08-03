package app

import (
	"testing"
	"time"

	"github.com/jhon/gorouter/internal/domain"
)

func rateLimit(id string, max float64, dur string) domain.KeyLimit {
	return domain.KeyLimit{ID: id, Kind: domain.KeyLimitRate, Max: max, Duration: dur}
}

func TestRateLimiter_NoLimits_AlwaysAllowed(t *testing.T) {
	rl := NewRateLimiter()
	ok, _ := rl.Allow("key", nil)
	if !ok {
		t.Fatal("empty limits should always allow")
	}
}

func TestRateLimiter_SlidingWindow(t *testing.T) {
	rl := NewRateLimiter()
	lim := []domain.KeyLimit{rateLimit("a", 2, "1h")}

	if ok, _ := rl.Allow("k", lim); !ok {
		t.Fatal("request 1 should pass")
	}
	if ok, _ := rl.Allow("k", lim); !ok {
		t.Fatal("request 2 should pass")
	}
	if ok, _ := rl.Allow("k", lim); ok {
		t.Fatal("request 3 should be blocked (max 2 per hour)")
	}
}

func TestRateLimiter_IndependentKeys(t *testing.T) {
	rl := NewRateLimiter()
	lim := []domain.KeyLimit{rateLimit("a", 1, "1h")}
	if ok, _ := rl.Allow("k1", lim); !ok {
		t.Fatal("k1 request 1 should pass")
	}
	if ok, _ := rl.Allow("k2", lim); !ok {
		t.Fatal("k2 request 1 should pass (independent window)")
	}
}

func TestRateLimiter_MultipleLimits_AND(t *testing.T) {
	rl := NewRateLimiter()
	// 2 per hour AND 3 per day.
	lim := []domain.KeyLimit{rateLimit("h", 2, "1h"), rateLimit("d", 3, "1d")}

	for i := 0; i < 2; i++ {
		if ok, _ := rl.Allow("k", lim); !ok {
			t.Fatalf("request %d should pass", i+1)
		}
	}
	// 3rd request exceeds the 2/hour window -> blocked even though the
	// daily window still has room.
	if ok, _ := rl.Allow("k", lim); ok {
		t.Fatal("3rd request should be blocked by the hourly limit")
	}
}

func TestRateLimiter_RetryAfter(t *testing.T) {
	rl := NewRateLimiter()
	lim := []domain.KeyLimit{rateLimit("a", 1, "1s")}
	if ok, _ := rl.Allow("k", lim); !ok {
		t.Fatal("first request should pass")
	}
	ok, retry := rl.Allow("k", lim)
	if ok {
		t.Fatal("second request should be blocked")
	}
	if retry <= 0 || retry > time.Second {
		t.Fatalf("expected retry between 0 and 1s, got %v", retry)
	}
}

func TestRateLimiter_EditedLimits_ResetWindow(t *testing.T) {
	rl := NewRateLimiter()
	lim := []domain.KeyLimit{rateLimit("a", 1, "1h")}
	if ok, _ := rl.Allow("k", lim); !ok {
		t.Fatal("first request should pass")
	}
	if ok, _ := rl.Allow("k", lim); ok {
		t.Fatal("second request should be blocked")
	}
	// Edit: raise the limit; the window for "a" is reused with the new max,
	// so the previously recorded request now fits again.
	lim2 := []domain.KeyLimit{rateLimit("a", 2, "1h")}
	if ok, _ := rl.Allow("k", lim2); !ok {
		t.Fatal("after raising limit, request should pass")
	}
}
