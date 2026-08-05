package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jhon/gorouter/internal/domain"
)

// requireAdmin is middleware that rejects non-admin dashboard users. It
// must run after requireDashboardToken (which injects the UserScope).
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scope := s.currentUser(r); scope == nil || scope.Role != domain.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// userPayload is the create/update DTO for users.
type userPayload struct {
	Name        string                  `json:"name"`
	Email       string                  `json:"email"`
	Password    string                  `json:"password"`
	Role        domain.UserRole         `json:"role"`
	Permissions *domain.UserPermissions `json:"permissions"`
}

// handleListUsers returns all users (admin only). Member users' passwords
// are never exposed (PasswordHash is json:"-").
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.currentUser(r) == nil || s.currentUser(r).Role != domain.RoleAdmin {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	users, err := s.Users.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	summaries, err := s.Users.ListAdminSummaries(r.Context(), users)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]UserListItem, 0, len(users))
	for _, u := range users {
		item := UserListItem{
			ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, Permissions: u.Permissions,
			CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
		}
		if summary, ok := summaries[u.ID]; ok {
			item.AllowedModels = summary.AllowedModels
			item.AllowedCombos = summary.AllowedCombos
			item.AllowedProviders = summary.AllowedProviders
			item.ApiKeysCount = summary.ApiKeysCount
			item.SessionActive = summary.SessionActive
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	var req userPayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	perms := domain.UserPermissions{}
	if req.Permissions != nil {
		perms = *req.Permissions
	}
	u, err := s.Users.CreateUser(r.Context(), req.Name, req.Email, req.Password, req.Role, perms)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	id := chi.URLParam(r, "id")
	var req userPayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.Users.UpdateUser(r.Context(), id, req.Name, req.Email, req.Password, req.Role, req.Permissions)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.Users.DeleteUser(r.Context(), id); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// accessPayload sets the grants of a single kind for a user.
type accessPayload struct {
	Kind domain.UserAccessKind `json:"kind"`
	IDs  []string              `json:"ids"`
}

func (s *Server) handleSetUserAccess(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	id := chi.URLParam(r, "id")
	var req accessPayload
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Users.SetAccess(r.Context(), req.Kind, id, req.IDs); err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	// Return the user with refreshed grants for the UI.
	u, err := s.Users.Get(r.Context(), id)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	if err := s.Users.LoadUserGrants(r.Context(), u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// isAdmin reports whether the request carries an admin UserScope.
func (s *Server) isAdmin(r *http.Request) bool {
	if scope := s.currentUser(r); scope != nil {
		return scope.Role == domain.RoleAdmin
	}
	return false
}
