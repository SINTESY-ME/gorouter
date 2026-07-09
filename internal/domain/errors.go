package domain

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel domain errors.
var (
	ErrNotFound        = errors.New("gorouter: not found")
	ErrAlreadyExists   = errors.New("gorouter: already exists")
	ErrValidation      = errors.New("gorouter: validation error")
	ErrUnauthorized    = errors.New("gorouter: unauthorized")
	ErrNoConnection    = errors.New("gorouter: no active connection for provider")
	ErrAllModelsFailed = errors.New("gorouter: all models in the combo failed")
)

// ShouldFallback decides whether a failed upstream response (or its parsed
// error) should trigger falling through to the next model in a combo or the
// next account in a connection pool.
//
// Rules (intentionally simple, mirrors 9router's errorConfig at a high level):
//   - 5xx and 429 -> fallback (transient upstream/rate limit)
//   - 408, network errors -> fallback (timeout / unreachable)
//   - 401, 403 -> fallback (try another account; refresh handled elsewhere)
//   - 400, 404, 422 -> do not fallback (client error, will fail everywhere)
func ShouldFallback(status int, err error) bool {
	if err != nil {
		return true // network / timeout
	}
	switch {
	case status >= 500 && status <= 599:
		return true
	case status == http.StatusTooManyRequests:
		return true
	case status == http.StatusRequestTimeout:
		return true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return true // try next account
	default:
		return false
	}
}

// ParseRetryAfter extracts a retry delay from a Retry-After header value.
// Supports both delta-seconds and HTTP-date forms. Returns 0 if absent or
// unparseable.
func ParseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}