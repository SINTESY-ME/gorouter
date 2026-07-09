package httpx

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jhon/gorouter/internal/app"
	"github.com/jhon/gorouter/internal/domain"
)

// authStatusResponse is the public /api/auth/status payload. The frontend
// uses it to decide between Setup (not configured), Login (configured but
// not authenticated), and the Dashboard (authenticated).
type authStatusResponse struct {
	Configured   bool `json:"configured"`
	Authenticated bool `json:"authenticated"`
}

// handleAuthStatus reports whether the dashboard password is configured and
// whether the request's bearer token is valid. This route is public (not
// behind requireDashboardToken) so the SPA can bootstrap its auth gate.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	configured, err := s.Auth.IsConfigured(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "auth status check failed")
		return
	}
	authenticated := false
	if configured {
		token := bearerToken(r)
		authenticated, _ = s.Auth.ValidateToken(r.Context(), token)
	}
	writeJSON(w, http.StatusOK, authStatusResponse{Configured: configured, Authenticated: authenticated})
}

// handleAuthSetup sets the initial dashboard password. Only succeeds when
// no password is configured yet.
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	if err := s.Auth.Setup(r.Context(), body.Password); err != nil {
		if err == app.ErrAuthAlreadyConfigured {
			writeError(w, http.StatusConflict, "dashboard password already configured")
			return
		}
		if isDomain(err, domain.ErrValidation) {
			writeError(w, http.StatusBadRequest, "invalid password")
			return
		}
		writeError(w, http.StatusInternalServerError, "setup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAuthLogin validates the password and returns 200 on success. The
// frontend stores the password itself as the bearer token for subsequent
// /api/* calls.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ok, err := s.Auth.Login(r.Context(), body.Password)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": body.Password})
}

// bearerToken extracts the bearer token from the Authorization header or the
// ?dashboard_token= query param.
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("dashboard_token")
}