package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jhon/gorouter/internal/domain"
)

// UserService is the dashboard use case for managing users and their access
// grants. Only admins call these methods (enforced in the HTTP layer).
type UserService struct {
	Users  domain.UserRepo
	Access domain.UserAccessRepo
}

// CreateUser creates a user. The plaintext password is hashed before
// storage. A nil permissions slice means "no member permissions".
func (s *UserService) CreateUser(ctx context.Context, username, password string, role domain.UserRole, perms domain.UserPermissions) (*domain.User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, domain.ErrValidation
	}
	if role != domain.RoleAdmin && role != domain.RoleMember {
		return nil, fmt.Errorf("%w: invalid role %q", domain.ErrValidation, role)
	}
	u := &domain.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: HashPassword(password),
		Role:         role,
		Permissions:  perms,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := s.Users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUser updates a user's role, permissions, and optionally password.
// A non-empty password resets it; role "" keeps the current role.
func (s *UserService) UpdateUser(ctx context.Context, id, username, password string, role domain.UserRole, perms *domain.UserPermissions) (*domain.User, error) {
	u, err := s.Users.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if username != "" {
		u.Username = username
	}
	if password != "" {
		u.PasswordHash = HashPassword(password)
	}
	if role != "" {
		if role != domain.RoleAdmin && role != domain.RoleMember {
			return nil, fmt.Errorf("%w: invalid role %q", domain.ErrValidation, role)
		}
		u.Role = role
	}
	if perms != nil {
		u.Permissions = *perms
	}
	u.UpdatedAt = time.Now()
	if err := s.Users.Update(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// DeleteUser removes a user and all their access grants. The last admin
// cannot be deleted (would lock out the dashboard).
func (s *UserService) DeleteUser(ctx context.Context, id string) error {
	u, err := s.Users.Get(ctx, id)
	if err != nil {
		return err
	}
	if u.IsAdmin() {
		admins := 0
		list, err := s.Users.List(ctx)
		if err != nil {
			return err
		}
		for _, uu := range list {
			if uu.IsAdmin() {
				admins++
			}
		}
		if admins <= 1 {
			return fmt.Errorf("%w: cannot delete the last admin", domain.ErrValidation)
		}
	}
	if err := s.Access.DeleteAll(ctx, id); err != nil {
		return err
	}
	return s.Users.Delete(ctx, id)
}

// List returns all users, oldest first.
func (s *UserService) List(ctx context.Context) ([]domain.User, error) {
	return s.Users.List(ctx)
}

// Get returns a user by ID.
func (s *UserService) Get(ctx context.Context, id string) (*domain.User, error) {
	return s.Users.Get(ctx, id)
}

// SetAccess replaces the grants of a given kind for a user.
func (s *UserService) SetAccess(ctx context.Context, kind domain.UserAccessKind, userID string, resourceIDs []string) error {
	switch kind {
	case domain.UserAccessProvider, domain.UserAccessModel, domain.UserAccessCombo:
	default:
		return fmt.Errorf("%w: invalid access kind %q", domain.ErrValidation, kind)
	}
	return s.Access.Set(ctx, kind, userID, resourceIDs)
}

// LoadUserGrants fills the AllowedModels/Combos/Providers fields on a user
// from the access tables. Admin users ignore grants (they see everything).
func (s *UserService) LoadUserGrants(ctx context.Context, u *domain.User) error {
	if u.IsAdmin() {
		return nil
	}
	for _, kind := range []struct {
		kind   domain.UserAccessKind
		target *[]string
	}{
		{domain.UserAccessModel, &u.AllowedModels},
		{domain.UserAccessCombo, &u.AllowedCombos},
		{domain.UserAccessProvider, &u.AllowedProviders},
	} {
		ids, err := s.Access.List(ctx, kind.kind, u.ID)
		if err != nil {
			return err
		}
		*kind.target = ids
	}
	return nil
}
