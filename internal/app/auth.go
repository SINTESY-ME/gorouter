// Package app provides application services that orchestrate the domain
// through the repository and executor ports. This file holds the dashboard
// auth service: password setup (first run) and login validation against
// either an env-provided token or a DB-stored hash.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/jhon/gorouter/internal/domain"
)

// SettingKeyDashboardPassword is the settings key for the dashboard
// password hash (sha256 hex). Health persistence will add more keys.
const SettingKeyDashboardPassword = "dashboard_password_hash"

// ErrAuthAlreadyConfigured is returned by Setup when a password is already set.
var ErrAuthAlreadyConfigured = errors.New("dashboard password already configured")

// AuthService manages the dashboard password. Two sources are supported:
//   - EnvToken: when non-empty (GOROUTER_DASHBOARD_TOKEN), it IS the password
//     and setup is skipped — the operator pre-configured it.
//   - Repo: a sha256(password) hash persisted in the settings table, set
//     via the first-run setup flow.
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
// sha256 hex hash.
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
// hash. Used by both login and the dashboard middleware.
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
	return HashPassword(token) == hash, nil
}

// HashPassword returns the sha256 hex digest of the password.
func HashPassword(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
}