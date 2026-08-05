package domain

import (
	"context"
	"time"
)

// UserRole distinguishes the two dashboard user kinds. Admins manage
// everything; members only see and edit resources they own or that were
// explicitly granted to them.
type UserRole string

const (
	// RoleAdmin sees and manages all resources (providers, combos, keys,
	// usage, settings, users).
	RoleAdmin UserRole = "admin"
	// RoleMember is scoped by ownership + grants (see UserPermissions and
	// the user_*_access tables).
	RoleMember UserRole = "member"
)

// UserPermissions is the per-member capability set the admin configures.
// Admin users implicitly have all permissions.
type UserPermissions struct {
	// CanManageOwnProviders allows creating/editing/deleting the member's
	// own providers and connections. Providers owned by the admin are
	// always read-only for members.
	CanManageOwnProviders bool `json:"can_manage_own_providers"`
	// CanCreateCombos allows creating/editing/deleting the member's own
	// combos. Admin combos are read-only for members.
	CanCreateCombos bool `json:"can_create_combos"`
	// CanManageCache grants access to the Performance/cache screens.
	// Without it the member inherits the admin's cache configuration.
	CanManageCache bool `json:"can_manage_cache"`
	// CanAccessSettings grants access to the Settings screen.
	CanAccessSettings bool `json:"can_access_settings"`
}

// User is a dashboard account. The first user (created during first-run
// setup) is always an admin; admins create additional admin/member users.
type User struct {
	ID           string          `json:"id" gorm:"primaryKey"`
	Name         string          `json:"name"`
	Email        string          `json:"email"`
	Username     string          `json:"-" gorm:"uniqueIndex"`
	Password     string          `json:"-" gorm:"-"` // plaintext; never persisted
	PasswordHash string          `json:"-" gorm:"column:password_hash;type:text"`
	Role         UserRole        `json:"role"`
	Permissions  UserPermissions `json:"permissions,omitempty" gorm:"serializer:json;type:text"`
	// Models/Combos/Providers visible to this user (member). Admins ignore
	// these (they see everything).
	AllowedModels    []string  `json:"allowed_models,omitempty" gorm:"-"`
	AllowedCombos    []string  `json:"allowed_combos,omitempty" gorm:"-"`
	AllowedProviders []string  `json:"allowed_providers,omitempty" gorm:"-"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// IsAdmin reports whether the user has the admin role.
func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// Session is a dashboard login session. The plaintext token is returned to
// the client exactly once at login; only its SHA-256 hash is stored. Token
// and user_id are unique because a user has one active session.
type Session struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	Token     string    `json:"-" gorm:"-"` // plaintext; never persisted
	TokenHash string    `json:"-" gorm:"uniqueIndex;column:token_hash"`
	UserID    string    `json:"user_id" gorm:"uniqueIndex;column:user_id"`
	CreatedAt time.Time `json:"created_at"`
}

// UserAccessKind enumerates the resource kinds a member can be granted.
type UserAccessKind string

const (
	UserAccessProvider UserAccessKind = "provider"
	UserAccessModel    UserAccessKind = "model"
	UserAccessCombo    UserAccessKind = "combo"
)

// UserAccess is a single grant: user X may see/use resource Y. One row per
// (kind, user, resource).
type UserAccess struct {
	Kind       UserAccessKind `json:"kind" gorm:"column:kind;primaryKey"`
	UserID     string         `json:"user_id" gorm:"column:user_id;primaryKey"`
	ResourceID string         `json:"resource_id" gorm:"column:resource_id;primaryKey"`
	CreatedAt  time.Time      `json:"created_at"`
}

type UserAdminSummary struct {
	UserID           string
	AllowedModels    []string
	AllowedCombos    []string
	AllowedProviders []string
	ApiKeysCount     int
	SessionActive    bool
}

type UserAdminSummaryRepo interface {
	List(ctx context.Context, userIDs []string) (map[string]UserAdminSummary, error)
}

// UserScope is the authenticated dashboard user attached to the request
// context by the session middleware. Repos use it to scope list queries to
// what the user can see. A nil scope means "no user context" (internal
// callers) and is treated like an admin.
type UserScope struct {
	UserID      string
	Role        UserRole
	Permissions UserPermissions
}

// userScopeCtxKey is the context key carrying the authenticated UserScope.
type userScopeCtxKey struct{}

// WithUserScope stores the authenticated user scope in the context.
func WithUserScope(ctx context.Context, scope *UserScope) context.Context {
	return context.WithValue(ctx, userScopeCtxKey{}, scope)
}

// UserScopeFrom retrieves the authenticated user scope. Returns nil when no
// dashboard session is present (internal/server-side calls) — callers treat
// nil like an admin (see everything).
func UserScopeFrom(ctx context.Context) *UserScope {
	if v, ok := ctx.Value(userScopeCtxKey{}).(*UserScope); ok {
		return v
	}
	return nil
}
