package httpx

import (
	"net/http"

	"github.com/jhon/gorouter/internal/infra/metrics"
)

// handleHealth returns a general status payload. Public (no auth): probes and
// load balancers use it to see the process is alive and healthy.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"uptime_seconds": int64(metrics.Default.Uptime().Seconds()),
	})
}

// handleLiveliness reports that the process is alive. Returns 200 always; a
// dead/hung process fails the probe (K8s restarts it).
func (s *Server) handleLiveliness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleReadiness reports that the router can serve traffic: the database is
// reachable and at least one provider connection is configured. Returns 503
// otherwise so orchestrators hold traffic until setup completes.
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if s.Models != nil {
		if _, err := s.Models.List(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
	}
	if s.Providers != nil {
		if conns, err := s.Providers.List(r.Context()); err == nil && len(conns) == 0 {
			writeError(w, http.StatusServiceUnavailable, "no providers configured")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}
