package httpx

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

func TestStatusForErrorUpstreamError(t *testing.T) {
	// A real upstream failure keeps its status instead of a generic 503.
	err := &domain.UpstreamError{Status: http.StatusTooManyRequests, Message: "rate limited"}
	if got := statusForError(err); got != http.StatusTooManyRequests {
		t.Fatalf("statusForError = %d, want 429", got)
	}
	// Wrapped UpstreamError still resolves via errors.As.
	if got := statusForError(errors.Join(errors.New("outer"), err)); got != http.StatusTooManyRequests {
		t.Fatalf("statusForError (wrapped) = %d, want 429", got)
	}
	// A zero-status UpstreamError falls through to sentinel matching.
	if got := statusForError(&domain.UpstreamError{}); got != http.StatusInternalServerError {
		t.Fatalf("statusForError(zero) = %d, want 500", got)
	}
}

func TestStatusForErrorHookReject(t *testing.T) {
	err := &domain.HookRejectError{Status: http.StatusForbidden, Message: "blocked"}
	if got := statusForError(err); got != http.StatusForbidden {
		t.Fatalf("statusForError = %d, want 403", got)
	}
}

func TestStatusForErrorSentinels(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{domain.ErrNotFound, http.StatusNotFound},
		{domain.ErrValidation, http.StatusBadRequest},
		{domain.ErrUnauthorized, http.StatusUnauthorized},
		{domain.ErrNoConnection, http.StatusServiceUnavailable},
		{domain.ErrAllModelsFailed, http.StatusBadGateway},
	}
	for _, tt := range tests {
		if got := statusForError(tt.err); got != tt.want {
			t.Errorf("statusForError(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}
