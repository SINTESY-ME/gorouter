// Package app provides application services that orchestrate the domain
// through the repository and executor ports. This file holds the dashboard
// auth service: password setup (first run) and login validation against
// either an env-provided token or a DB-stored hash.
package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/jhon/gorouter/internal/domain"
)

// SettingKeyDashboardPassword is the settings key for the dashboard
// password hash. New hashes are bcrypt (salt embedded in the string);
// legacy unsalted sha256 hex hashes are still accepted and migrated on
// the next successful login.
const SettingKeyDashboardPassword = "dashboard_password_hash"

// ErrAuthAlreadyConfigured is returned by Setup when a password is already set.
var ErrAuthAlreadyConfigured = errors.New("dashboard password already configured")

// AuthService manages the dashboard password. Two sources are supported:
//   - EnvToken: when non-empty (GOROUTER_DASHBOARD_TOKEN), it IS the password
//     and setup is skipped — the operator pre-configured it.
//   - Repo: a bcrypt hash persisted in the settings table, set via the
//     first-run setup flow.
//
// ValidateToken accepts a candidate and returns true if it matches either
// source. The token that the frontend stores after login is the plaintext
// password; all /api/* calls send it as Bearer.
type AuthService struct {
	EnvToken string
	Repo     domain.SettingRepo
}

// IsConfigured reports whether a dashboard password is set (env or DB).
func (a *AuthService) IsConfigured(ctx context.Context) (bool, error) {
	if a.EnvToken != "" {
		return true, nil
	}
	if a.Repo == nil {
		return false, nil
	}
	return a.Repo.Has(ctx, SettingKeyDashboardPassword)
}

// Setup stores the initial password. It only succeeds when no password is
// configured yet (env empty AND no DB hash). The password is stored as a
// salted bcrypt hash.
func (a *AuthService) Setup(ctx context.Context, password string) error {
	if password == "" {
		return domain.ErrValidation
	}
	if a.EnvToken != "" {
		return ErrAuthAlreadyConfigured
	}
	if a.Repo == nil {
		return errors.New("settings repo unavailable")
	}
	has, err := a.Repo.Has(ctx, SettingKeyDashboardPassword)
	if err != nil {
		return err
	}
	if has {
		return ErrAuthAlreadyConfigured
	}
	return a.Repo.Set(ctx, SettingKeyDashboardPassword, HashPassword(password))
}

// Login validates the password against the configured source. Returns true
// on success.
func (a *AuthService) Login(ctx context.Context, password string) (bool, error) {
	return a.ValidateToken(ctx, password)
}

// ValidateToken checks a candidate token against the env var or the DB
// hash. Used by both login and the dashboard middleware. When a legacy
// (unsalted sha256) hash matches, it is transparently re-hashed to bcrypt
// so the next login uses the stronger format.
func (a *AuthService) ValidateToken(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	if a.EnvToken != "" {
		return token == a.EnvToken, nil
	}
	if a.Repo == nil {
		return false, nil
	}
	hash, err := a.Repo.Get(ctx, SettingKeyDashboardPassword)
	if err != nil {
		return false, nil
	}
	if hash == "" {
		return false, nil
	}
	if !ComparePassword(token, hash) {
		return false, nil
	}
	if isLegacyHash(hash) {
		// Migration: upgrade the stored hash in place on a successful
		// legacy login. Non-fatal if the write fails.
		_ = a.Repo.Set(ctx, SettingKeyDashboardPassword, HashPassword(token))
	}
	return true, nil
}

// bcryptCost is the work factor used for dashboard password hashing.
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
