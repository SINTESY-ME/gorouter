package httpx

import (
	"net/http"
	"strconv"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
)

const (
	rtkSettingKey   = "rtk_enabled"
	cacheSettingKey = "cache_enabled"
)

// handleGetSettings returns user-configurable gorouter settings (RTK + cache
// toggles). Both persist across restarts via SettingRepo.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	rtk, cache := false, false
	if s.Settings != nil {
		if v, err := s.Settings.Get(r.Context(), rtkSettingKey); err == nil {
			rtk = v == "true"
		}
		if v, err := s.Settings.Get(r.Context(), cacheSettingKey); err == nil {
			cache = v == "true"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rtk_enabled":   rtk,
		"cache_enabled": cache,
	})
}

// handleUpdateSettings updates gorouter settings. Both toggles are live —
// the compressor and cache are wired/unwired without a restart.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RTKEnabled   *bool `json:"rtk_enabled"`
		CacheEnabled *bool `json:"cache_enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.RTKEnabled != nil {
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), rtkSettingKey, strconv.FormatBool(*req.RTKEnabled)); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if *req.RTKEnabled {
			if s.Router.Compressor == nil && s.RTKCompressorFactory != nil {
				s.Router.Compressor = s.RTKCompressorFactory()
			}
		} else {
			s.Router.Compressor = nil
		}
	}
	if req.CacheEnabled != nil {
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), cacheSettingKey, strconv.FormatBool(*req.CacheEnabled)); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if *req.CacheEnabled {
			if s.Router.Cache == nil && s.CacheFactory != nil {
				s.Router.Cache = app.NewCacheService(s.CacheFactory())
			}
		} else {
			s.Router.Cache = nil
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

var _ domain.SettingRepo