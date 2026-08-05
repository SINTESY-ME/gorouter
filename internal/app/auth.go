// Package app provides application services that orchestrate the domain
// through the repository and executor ports. This file holds the dashboard
// auth service: user setup (first run), login via email/password with
// session tokens, and session validation.
package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/google/uuid"

	"github.com/jhon/gorouter/internal/domain"
)

// SettingKeyDashboardPassword is the legacy settings key for the single
// dashboard password hash. New installs use the users table; existing
// installs are migrated to an admin user on the first successful login.
const SettingKeyDashboardPassword = "dashboard_password_hash"

// ErrAuthAlreadyConfigured is returned by Setup when a password is already set.
var ErrAuthAlreadyConfigured = errors.New("dashboard password already configured")

// ErrInvalidCredentials is returned when an email/password pair is wrong.
var ErrInvalidCredentials = errors.New("invalid email or password")

// AuthService manages dashboard authentication. Two sources are supported:
//   - EnvToken (GOROUTER_DASHBOARD_TOKEN): a master token that authenticates
//     as the admin without a session. Kept for backwards compatibility.
//   - Users: a users table with per-user bcrypt password hashes and
//     session tokens. This is the primary mode going forward.
//
// Login always produces a session token (never returns the password). The
// frontend stores the session token and sends it as Bearer on /api/* calls.
type AuthService struct {
	EnvToken string
	Users    domain.UserRepo
	Sessions domain.SessionRepo
	Setting  domain.SettingRepo // legacy single-password migration
}

// IsConfigured reports whether authentication is set up: an env token, any
// users, or a legacy password hash.
func (a *AuthService) IsConfigured(ctx context.Context) (bool, error) {
	if a.EnvToken != "" {
		return true, nil
	}
	if a.Users != nil {
		users, err := a.Users.List(ctx)
		if err != nil {
			return false, err
		}
		if len(users) > 0 {
			return true, nil
		}
	}
	if a.Setting != nil {
		has, err := a.Setting.Has(ctx, SettingKeyDashboardPassword)
		if err != nil {
			return false, err
		}
		return has, nil
	}
	return false, nil
}

// Setup creates the initial admin user. It only succeeds when no
// authentication is configured yet (env token empty, no users, no legacy
// hash). The password is stored as a salted bcrypt hash.
func (a *AuthService) Setup(ctx context.Context, name, email, password string) error {
	if strings.TrimSpace(name) == "" || password == "" {
		return domain.ErrValidation
	}
	email, err := normalizeAuthEmail(email)
	if err != nil {
		return domain.ErrValidation
	}
	configured, err := a.IsConfigured(ctx)
	if err != nil {
		return err
	}
	if configured {
		return ErrAuthAlreadyConfigured
	}
	_, err = a.createAdmin(ctx, name, email, password)
	return err
}

// Login validates an email/password pair and, on success, creates a fresh
// session for the user. Returns the session (with the plaintext token) or
// ErrInvalidCredentials.
func (a *AuthService) Login(ctx context.Context, email, password string) (*domain.Session, error) {
	user, err := a.authenticate(ctx, email, password)
	if err != nil {
		return nil, err
	}
	token := randomToken()
	sess := &domain.Session{
		ID:        uuid.NewString(),
		Token:     token,
		TokenHash: hashToken(token),
		UserID:    user.ID,
		CreatedAt: time.Now(),
	}
	if err := a.Sessions.Create(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// ValidateSession resolves a presented session token to its user. Returns
// nil when the token is invalid or the user no longer exists.
func (a *AuthService) ValidateSession(ctx context.Context, token string) (*domain.User, error) {
	if token == "" {
		return nil, nil
	}
	sess, err := a.Sessions.GetByTokenHash(ctx, hashToken(token))
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, nil
	}
	return a.Users.Get(ctx, sess.UserID)
}

// Logout revokes the user's session (if any).
func (a *AuthService) Logout(ctx context.Context, userID string) error {
	return a.Sessions.DeleteByUser(ctx, userID)
}

// authenticate validates the credentials. Order:
//  1. Legacy migration: if no users exist and a legacy password hash is
//     stored, a match migrates it into an admin user (in place).
//  2. Env token: if set and the password matches it, authenticates as admin
//     (creating the admin user lazily if needed).
//  3. Users table: email + bcrypt password.
func (a *AuthService) authenticate(ctx context.Context, email, password string) (*domain.User, error) {
	users, err := a.Users.List(ctx)
	if err != nil {
		return nil, err
	}

	// Legacy single-password migration: no users yet + a stored hash.
	if len(users) == 0 && a.Setting != nil {
		if hash, gerr := a.Setting.Get(ctx, SettingKeyDashboardPassword); gerr == nil && hash != "" {
			if ComparePassword(password, hash) {
				user, cerr := a.createAdmin(ctx, defaultAdminName, defaultAdminEmail, password)
				if cerr != nil {
					return nil, cerr
				}
				_ = a.Setting.Set(ctx, SettingKeyDashboardPassword, "")
				return user, nil
			}
		}
	}

	// Env token master: treats the configured admin as authenticated.
	if a.EnvToken != "" && password == a.EnvToken {
		u := findByEmail(users, email)
		if u == nil {
			u = findByEmail(users, defaultAdminEmail)
		}
		if u == nil {
			u, err = a.createAdmin(ctx, defaultAdminName, defaultAdminEmail, randomToken())
			if err != nil {
				return nil, err
			}
		}
		return u, nil
	}

	email, err = normalizeAuthEmail(email)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	u := findByEmail(users, email)
	if u == nil || !ComparePassword(password, u.PasswordHash) {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}

func (a *AuthService) createAdmin(ctx context.Context, name, email, password string) (*domain.User, error) {
	u := &domain.User{
		ID:           uuid.NewString(),
		Name:         name,
		Email:        email,
		Username:     email,
		PasswordHash: HashPassword(password),
		Role:         domain.RoleAdmin,
		Permissions:  domain.UserPermissions{},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := a.Users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func findByEmail(users []domain.User, email string) *domain.User {
	for i := range users {
		if users[i].Email == email {
			return &users[i]
		}
	}
	return nil
}

func normalizeAuthEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", err
	}
	return email, nil
}

const (
	defaultAdminName  = "Admin"
	defaultAdminEmail = "admin@localhost"
)

// hashToken returns the SHA-256 hex digest of a session token, stored at
// rest so a database compromise does not leak active session tokens.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// randomToken returns a fresh random session token.
func randomToken() string {
	return uuid.NewString() + uuid.NewString()
}

// bcryptCost is the work factor used for password hashing.
const bcryptCost = bcrypt.DefaultCost

// HashPassword returns a salted bcrypt hash of the password. The salt and
// cost are embedded in the returned string, so no separate salt storage is
// needed. Bcrypt caps input at 72 bytes; longer passwords fall back to the
// legacy sha256 hash (still better than a hard failure during setup).
func HashPassword(password string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return legacyHash(password)
	}
	return string(h)
}

// ComparePassword checks a candidate against a stored hash. Accepts both
// bcrypt hashes ("$2a$..."/"$2b$..."/"$2y$...") and legacy unsalted sha256
// hex hashes.
func ComparePassword(candidate, stored string) bool {
	if isLegacyHash(stored) {
		return legacyCompare(candidate, stored)
	}
	return bcrypt.CompareHashAndPassword([]byte(stored), []byte(candidate)) == nil
}

func isLegacyHash(stored string) bool {
	return !strings.HasPrefix(stored, "$2")
}

func legacyCompare(candidate, stored string) bool {
	got := legacyHash(candidate)
	return subtle.ConstantTimeCompare([]byte(got), []byte(stored)) == 1
}

func legacyHash(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
}
