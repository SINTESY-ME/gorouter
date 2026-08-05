package httpx

import (
	"context"
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
	Configured    bool `json:"configured"`
	Authenticated bool `json:"authenticated"`
}

// handleAuthStatus reports whether dashboard auth is configured and whether
// the request's bearer token is a valid session. This route is public (not
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
		if token != "" {
			user, _ := s.Auth.ValidateSession(r.Context(), token)
			authenticated = user != nil
		}
		// Env-token master still authenticates without a session row.
		if !authenticated && s.Auth.EnvToken != "" && s.Auth.EnvToken == token {
			authenticated = true
		}
	}
	writeJSON(w, http.StatusOK, authStatusResponse{Configured: configured, Authenticated: authenticated})
}

// handleAuthSetup creates the initial admin user. Only succeeds when no
// authentication is configured yet.
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
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
	if err := s.Auth.Setup(r.Context(), body.Name, body.Email, body.Password); err != nil {
		if err == app.ErrAuthAlreadyConfigured {
			writeError(w, http.StatusConflict, "dashboard auth already configured")
			return
		}
		if isDomain(err, domain.ErrValidation) {
			writeError(w, http.StatusBadRequest, "invalid name, email, or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "setup failed")
		return
	}
	// Auto-login so the first admin lands directly in the dashboard.
	s.respondLogin(r.Context(), w, body.Email, body.Password)
}

// handleAuthLogin validates username/password and returns a session token.
// The frontend stores the token as the bearer for subsequent /api/* calls.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s.respondLogin(r.Context(), w, body.Email, body.Password)
}

func (s *Server) respondLogin(ctx context.Context, w http.ResponseWriter, email, password string) {
	sess, err := s.Auth.Login(ctx, email, password)
	if err != nil {
		if err == app.ErrInvalidCredentials {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": sess.Token})
}

// handleAuthLogout revokes the current session.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if u := s.currentUser(r); u != nil && s.Auth != nil {
		_ = s.Auth.Logout(r.Context(), u.UserID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// meResponse is the current-user payload for /api/auth/me.
type meResponse struct {
	ID    string                 `json:"id"`
	Name  string                 `json:"name"`
	Email string                 `json:"email"`
	Role  domain.UserRole        `json:"role"`
	Perms domain.UserPermissions `json:"permissions"`
}

// handleAuthMe returns the authenticated dashboard user. Public route that
// 401s when no session is valid.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	scope := s.currentUser(r)
	if scope == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	name, email := scope.UserID, ""
	if scope.UserID != "__env__" {
		if u, err := s.Users.Get(r.Context(), scope.UserID); err == nil && u != nil {
			name, email = u.Name, u.Email
		}
	} else {
		name, email = "Admin", "admin@localhost"
	}
	writeJSON(w, http.StatusOK, meResponse{
		ID:    scope.UserID,
		Name:  name,
		Email: email,
		Role:  scope.Role,
		Perms: scope.Permissions,
	})
}

// bearerToken extracts the bearer token from the Authorization header or the
// ?dashboard_token= query param.
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("dashboard_token")
}
