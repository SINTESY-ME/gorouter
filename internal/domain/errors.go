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
	ErrForbidden       = errors.New("gorouter: forbidden")
	ErrNoConnection    = errors.New("gorouter: no active connection for provider")
	ErrAllModelsFailed = errors.New("gorouter: all models in the combo failed")
)

// ShouldFallback decides whether a failed upstream response (or its parsed
// error) should trigger falling through to the next model in a combo or the
// next account in a connection pool.
//
// Fallback is only attempted when the failure might resolve on another model
// or account: transient infrastructure errors (5xx, 408, 429), account-level
// failures (401/403/402), or a model that is gone on this provider (404).
// Deterministic client errors (400, 422, 415, …) fail on every provider the
// same way, so falling through would only burn requests and latency — they
// are returned to the client instead.
func ShouldFallback(status int, err error) bool {
	if err != nil {
		return true // network / timeout
	}
	switch {
	case status >= 500 && status <= 599:
		return true // upstream transient failure
	case status == http.StatusTooManyRequests:
		return true // rate limited; try next account/model
	case status == http.StatusRequestTimeout:
		return true // timeout / unavailable
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return true // bad account credentials; try next account
	case status == http.StatusPaymentRequired:
		return true // out of credit on this key; try next account
	case status == http.StatusNotFound:
		return true // model deprecated/removed on this provider; next may work
	default:
		return false // deterministic client error: do not fall through
	}
}

// ShouldFallbackWithMessage is ShouldFallback plus response-body awareness: a
// 400 whose message signals credit exhaustion on this key/account falls
// through to the next connection/model instead of failing the request.
// Some upstreams (e.g. CommandCode) report an empty balance as
// 400 "You have insufficient credits..." instead of 402, and a pure
// status-based check would wrongly treat it as a deterministic client error.
func ShouldFallbackWithMessage(status int, message string) bool {
	if ShouldFallback(status, nil) {
		return true
	}
	return status == http.StatusBadRequest && isCreditExhausted(message)
}

// creditExhaustedMarkers matches upstream "out of credit" failures reported
// with a non-402 status. Kept narrow: a 400 carrying one of these messages
// is account-level, never a malformed request.
var creditExhaustedMarkers = []string{
	"insufficient credit",
	"insufficient balance",
	"out of credit",
	"purchase more credit",
	"top up",
}

// isCreditExhausted reports whether an upstream error message signals an
// empty account balance even though the HTTP status is not 402.
func isCreditExhausted(message string) bool {
	m := strings.ToLower(message)
	for _, marker := range creditExhaustedMarkers {
		if strings.Contains(m, marker) {
			return true
		}
	}
	return false
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
