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
	ErrNotFound         = errors.New("gorouter: not found")
	ErrAlreadyExists    = errors.New("gorouter: already exists")
	ErrValidation       = errors.New("gorouter: validation error")
	ErrUnauthorized     = errors.New("gorouter: unauthorized")
	ErrForbidden        = errors.New("gorouter: forbidden")
	ErrNoConnection     = errors.New("gorouter: no active connection for provider")
	ErrProviderDisabled = errors.New("gorouter: provider is disabled")
	ErrAllModelsFailed  = errors.New("gorouter: all models in the combo failed")
)

// ShouldFallback decides whether a failed upstream response (or its parsed
// error) should trigger falling through to the next model in a combo or the
// next account in a connection pool.
//
// Any upstream failure is eligible for fallback by default. A different
// policy must be selected explicitly by the caller; the default must not
// strand a combo on a provider-specific 4xx response.
func ShouldFallback(status int, err error) bool {
	return err != nil || status >= http.StatusBadRequest
}

// ShouldFallbackWithMessage preserves the message-aware API used by the
// router. The default policy is status-independent: every upstream error
// falls through, regardless of its response body.
func ShouldFallbackWithMessage(status int, message string) bool {
	return ShouldFallback(status, nil)
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
