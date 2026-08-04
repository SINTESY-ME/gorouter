package domain

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestShouldFallback(t *testing.T) {
	tests := []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{name: "network error", err: errors.New("boom"), want: true},
		{name: "timeout error", err: context.DeadlineExceeded, want: true},
		{name: "500 internal", status: 500, want: true},
		{name: "502 bad gateway", status: 502, want: true},
		{name: "503 unavailable", status: 503, want: true},
		{name: "429 rate limited", status: 429, want: true},
		{name: "408 request timeout", status: 408, want: true},
		{name: "401 unauthorized", status: 401, want: true},
		{name: "403 forbidden", status: 403, want: true},
		{name: "402 payment required", status: 402, want: true},
		{name: "404 not found", status: 404, want: true},
		{name: "400 bad request", status: 400, want: false},
		{name: "422 unprocessable", status: 422, want: false},
		{name: "415 unsupported media", status: 415, want: false},
		{name: "200 ok", status: 200, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldFallback(tt.status, tt.err); got != tt.want {
				t.Errorf("ShouldFallback(%d, %v) = %v, want %v", tt.status, tt.err, got, tt.want)
			}
		})
	}
}

func TestShouldFallbackCoversTransientStatuses(t *testing.T) {
	// Every status that isTransientStatus treats as retryable must also
	// trigger fallback, so the retry path and the fallback path agree.
	transient := []int{
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	}
	for _, status := range transient {
		if !ShouldFallback(status, nil) {
			t.Errorf("transient status %d must trigger fallback", status)
		}
	}
}
