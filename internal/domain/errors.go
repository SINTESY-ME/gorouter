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
// Any error (status >= 400 or network error) from an upstream call triggers
// fallback — the next model or connection may have different capabilities,
// limits, or account state.
func ShouldFallback(status int, err error) bool {
	if err != nil {
		return true // network / timeout
	}
	// Currently all 4xx/5xx trigger fallback. Preserving fine-grained rules below
	// for reference or if selective fallback is needed in the future:
	//
	// switch {
	// case status >= 500 && status <= 599:
	// 	return true
	// case status == http.StatusTooManyRequests:
	// 	return true
	// case status == http.StatusRequestTimeout:
	// 	return true
	// case status == http.StatusUnauthorized || status == http.StatusForbidden:
	// 	return true // try next account
	// case status == http.StatusPaymentRequired:
	// 	return true // try next account (402 = out of credit on this key)
	// case status == http.StatusNotFound:
	// 	return true // model deprecated/removed on this provider; next may work
	// case status == http.StatusBadRequest:
	// 	return true // model may not support this request format; next model may
	// default:
	// 	return false
	// }
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