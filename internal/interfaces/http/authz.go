package httpx

import (
	"net/http"

	"github.com/jhon/gorouter/internal/domain"
)

// createdByFor returns the owner ID to stamp on a resource the caller is
// creating. Members own what they create; admins create global resources
// (created_by = "") so every member can see them.
func (s *Server) createdByFor(r *http.Request) string {
	if scope := s.currentUser(r); scope != nil && scope.Role == domain.RoleMember {
		return scope.UserID
	}
	return ""
}

// canManageResource reports whether the caller may create/modify resources
// of the given kind. Admins always can; members only when the matching
// permission flag is set.
func (s *Server) canManageResource(r *http.Request, kind domain.UserAccessKind) bool {
	scope := s.currentUser(r)
	if scope == nil || scope.Role == domain.RoleAdmin {
		return true
	}
	switch kind {
	case domain.UserAccessProvider:
		return scope.Permissions.CanManageOwnProviders
	case domain.UserAccessCombo:
		return scope.Permissions.CanCreateCombos
	}
	return false
}

// ownsResource reports whether the caller owns the given resource. Admins
// own everything (they may edit/delete any resource). Members own only what
// they created.
func (s *Server) ownsResource(r *http.Request, createdBy string) bool {
	scope := s.currentUser(r)
	if scope == nil || scope.Role == domain.RoleAdmin {
		return true
	}
	return createdBy == scope.UserID
}

// canViewResource reports whether the caller may view a resource the admin
// did not create. Admins see everything; members see resources they own or
// that were explicitly granted (the grant check happens at the repo layer).
// This helper covers ownership-only decisions in handlers.
func (s *Server) canViewResource(r *http.Request, createdBy string) bool {
	scope := s.currentUser(r)
	if scope == nil || scope.Role == domain.RoleAdmin {
		return true
	}
	return createdBy == scope.UserID
}
