package httpx

import (
	"net/http"
)

// handleCacheStats returns current response cache statistics (entries, hits,
// misses). Returns 404 when caching is disabled.
func (s *Server) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	if s.Cache == nil || !s.Cache.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	stats := s.Cache.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"entries": stats.Entries,
		"hits":    stats.Hits,
		"misses":  stats.Misses,
	})
}

// handleCacheFlush removes all cached entries.
func (s *Server) handleCacheFlush(w http.ResponseWriter, r *http.Request) {
	if s.Cache == nil || !s.Cache.Enabled() {
		writeError(w, http.StatusNotFound, "cache is not enabled")
		return
	}
	s.Cache.Flush(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"status": "flushed"})
}