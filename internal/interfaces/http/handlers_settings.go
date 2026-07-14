package httpx

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jhon/gorouter/internal/domain"
)

// rtkSettingKey is the persisted settings key for RTK enable/disable.
const rtkSettingKey = "rtk_enabled"

// handleGetSettings returns user-configurable gorouter settings. Currently
// only RTK toggle; expand as more user-facing settings are added.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	rtk := false
	if s.Settings != nil {
		if v, err := s.Settings.Get(r.Context(), rtkSettingKey); err == nil {
			rtk = v == "true"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rtk_enabled": rtk,
	})
}

// handleUpdateSettings updates gorouter settings. Currently only RTK; the
// compressor is wired/unwired live (no restart needed).
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RTKEnabled *bool `json:"rtk_enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.RTKEnabled != nil {
		val := strconv.FormatBool(*req.RTKEnabled)
		if s.Settings != nil {
			if err := s.Settings.Set(r.Context(), rtkSettingKey, val); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		// Live toggle: wire/unwire the compressor on the router.
		if *req.RTKEnabled {
			if s.Router.Compressor == nil && s.RTKCompressorFactory != nil {
				s.Router.Compressor = s.RTKCompressorFactory()
			}
		} else {
			s.Router.Compressor = nil
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

// SettingRepo is unused here; imported to silence linters when the package
// is extended. Remove the line below if it becomes a build error.
var _ = json.Marshal
var _ domain.SettingRepo