package httpx

import (
	"net/http"

	"github.com/jhon/gorouter/internal/infra/metrics"
)

// handleMetrics serves the Prometheus text exposition. It is intentionally
// public (scrapers do not carry a gorouter API key) and cheap: counters are
// in-memory and gauges are evaluated per scrape from existing state.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	metrics.Default.WritePrometheus(w, r)
}
