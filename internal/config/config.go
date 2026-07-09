// Package config loads runtime configuration from the environment with
// sensible defaults. No flags, no file parsing — keep it boring.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config is the resolved application configuration.
type Config struct {
	Port        string
	HomeDir     string
	DBPath      string
	DBDriver    string // "sqlite" (default) or "postgres"
	DBDSN       string // postgres connection string (when DBDriver=="postgres")
	KeySecret   string // HMAC secret for API key CRC generation
	RequireKey  bool
	UpstreamTimeoutSeconds int
	DashboardToken string // if non-empty, /api/* requires this bearer token
}

// FromEnv builds Config from environment variables, defaults applied.
// KeySecret is read from GOROUTER_KEY_SECRET, or persisted to
// <HomeDir>/key_secret on first run so a random one survives restarts.
func FromEnv() (*Config, error) {
	home := envOr("GOROUTER_HOME", filepath.Join(homeDir(), ".gorouter"))
	cfg := &Config{
		Port:        envOr("GOROUTER_PORT", "20128"),
		HomeDir:     home,
		DBPath:      envOr("GOROUTER_DB", filepath.Join(home, "gorouter.db")),
		DBDriver:    envOr("GOROUTER_DB_DRIVER", "sqlite"),
		DBDSN:       os.Getenv("GOROUTER_DB_DSN"),
		RequireKey:  envBool("GOROUTER_REQUIRE_KEY", true),
		UpstreamTimeoutSeconds: envInt("GOROUTER_UPSTREAM_TIMEOUT", 600),
		DashboardToken: os.Getenv("GOROUTER_DASHBOARD_TOKEN"),
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("create home dir: %w", err)
	}
	secretFile := filepath.Join(home, "key_secret")
	if s := os.Getenv("GOROUTER_KEY_SECRET"); s != "" {
		cfg.KeySecret = s
	} else if b, err := os.ReadFile(secretFile); err == nil && len(b) > 0 {
		cfg.KeySecret = string(b)
	} else {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generate key secret: %w", err)
		}
		cfg.KeySecret = hex.EncodeToString(buf)
		_ = os.WriteFile(secretFile, []byte(cfg.KeySecret), 0o600)
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}