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

func (s *Server) handleSemanticCacheStats(w http.ResponseWriter, r *http.Request) {
	if s.SemanticCache == nil || !s.SemanticCache.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	stats := s.SemanticCache.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"mode":    s.SemanticCache.Mode(),
		"entries": stats.Entries,
		"hits":    stats.Hits,
		"misses":  stats.Misses,
	})
}

func (s *Server) handleSemanticCacheFlush(w http.ResponseWriter, r *http.Request) {
	if s.SemanticCache == nil {
		writeError(w, http.StatusNotFound, "semantic cache is not configured")
		return
	}
	s.SemanticCache.Flush(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"status": "flushed"})
}

// handleSavings returns aggregated savings (cache + RTK) from the database
// so the data survives restarts. Reads from usage_entries where savings
// fields are populated. Falls back to in-memory tracker when Usage is nil.
func (s *Server) handleSavings(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "60d"
	}
	var apiKeyID string
	if keyID := r.URL.Query().Get("api_key_id"); keyID != "" {
		apiKeyID = keyID
	}
	if s.Usage != nil {
		agg, err := s.Usage.Repo.SavingsStats(r.Context(), period, apiKeyID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, agg)
		return
	}
	if s.Savings != nil {
		writeJSON(w, http.StatusOK, s.Savings.Stats())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cache_hits":         0,
		"cache_tokens_saved": 0,
		"cache_cost_saved":   0,
		"rtk_compressions":   0,
		"rtk_bytes_saved":    0,
		"rtk_tokens_saved":   0,
		"rtk_cost_saved":     0,
	})
}
