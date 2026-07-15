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

// handleSavings returns cumulative token/byte savings from the response cache
// and RTK request compression. Returns zeros when the tracker is nil.
func (s *Server) handleSavings(w http.ResponseWriter, r *http.Request) {
	if s.Savings == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"cache_hits":        0,
			"cache_tokens_saved": 0,
			"rtk_compressions":  0,
			"rtk_bytes_saved":   0,
			"rtk_tokens_saved":  0,
		})
		return
	}
	writeJSON(w, http.StatusOK, s.Savings.Stats())
}