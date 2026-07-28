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
//   - 401, 403, 402 -> fallback (try another account; 402 means this
//     account/key is out of credit, the next connection may still work)
//   - 404 -> fallback (model may be deprecated/removed on this provider,
//     but the next model/combo may still work)
//   - 400 -> fallback in combo context: the model may not support the
//     request format (tool calls, vision, response_format), but the next
//     model in the combo may handle it fine.
//   - 422 -> do not fallback (validation error, will fail everywhere)
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
	case status == http.StatusPaymentRequired:
		return true // try next account (402 = out of credit on this key)
	case status == http.StatusNotFound:
		return true // model deprecated/removed on this provider; next may work
	case status == http.StatusBadRequest:
		return true // model may not support this request format; next model may
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