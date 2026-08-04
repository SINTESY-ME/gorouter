package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHealthEndpoints verifies the health/readiness probe routes respond 200
// through the full router with a zero-valued Server.
func TestHealthEndpoints(t *testing.T) {
	s := &Server{}
	for _, path := range []string{"/health", "/health/liveliness", "/health/readiness"} {
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, rec.Code)
		}
	}
}
