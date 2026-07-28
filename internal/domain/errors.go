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
// Any error from an upstream call should trigger fallback — the next model
// or connection may have different capabilities, limits, or account state.
func ShouldFallback(status int, err error) bool {
	if err != nil {
		return true // network / timeout
	}
	return status >= 400
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