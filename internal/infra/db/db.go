// Package db opens a database (SQLite or Postgres) via GORM and exposes
// repos that implement the domain ports. GORM handles AutoMigrate,
// placeholder differences, and error translation (ErrDuplicatedKey,
// ErrRecordNotFound) so the repos stay trivial.
package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/apikey"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open opens a database for the given driver, applies AutoMigrate, and
// returns a *gorm.DB.
//
//   - driver "sqlite" (or ""): dsn is a filesystem path; the parent
//     directory is created; single writer connection to avoid "database is
//     locked".
//   - driver "postgres": dsn is a libpq connection string (e.g.
//     "postgres://user:pass@host:5432/dbname?sslmode=disable").
func Open(ctx context.Context, driver, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{
		TranslateError: true, // map duplicate-key / not-found to gorm.Err*
		Logger:         logger.Default.LogMode(logger.Warn),
	}
	var db *gorm.DB
	var err error
	switch driver {
	case "", "sqlite":
		if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
		// modernc.org/sqlite (via glebarez) expects a "file:" URI; without
		// it the database may open read-only.
		if !strings.HasPrefix(dsn, "file:") {
			dsn = "file:" + dsn
		}
		db, err = gorm.Open(sqlite.Open(dsn), cfg)
	case "postgres":
		if dsn == "" {
			return nil, fmt.Errorf("postgres dsn is empty (set GOROUTER_DB_DSN)")
		}
		db, err = gorm.Open(postgres.Open(dsn), cfg)
	default:
		return nil, fmt.Errorf("unsupported db driver %q (want sqlite|postgres)", driver)
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", driver, err)
	}
	// SQLite serializes writes; one connection avoids "database is locked".
	if driver == "" || driver == "sqlite" {
		if sqlDB, e := db.DB(); e == nil {
			sqlDB.SetMaxOpenConns(1)
		}
	}
	if err := db.AutoMigrate(&domain.Connection{}, &domain.ProviderConfig{}, &domain.Combo{}, &domain.ApiKey{}, &domain.UsageEntry{}, &domain.ComboExecution{}, &domain.ModelEntry{}, &domain.Setting{}); err != nil {
		_ = Close(db)
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}
	// Drop the old combo_name column from usage_entries. Combo membership
	// is now tracked in the combo_executions table.
	dropColumnIfExists(db, "usage_entries", "combo_name")
	// Backfill request_id for entries created before the request_id column
	// existed. Each old entry becomes its own request group.
	db.Exec("UPDATE usage_entries SET request_id = CAST(id AS TEXT) WHERE request_id IS NULL OR request_id = ''")
	// Backfill: create ProviderConfig rows for any provider_id that exists
	// in connections but has no ProviderConfig row yet. This ensures
	// existing deployments get the Provider entity without manual migration.
	backfillProviderConfigs(db)
	// Drop the legacy scalar limit columns from api_keys. Limits are now
	// stored as a JSON slice on the ApiKey.Limits field; these columns were
	// a short-lived earlier design.
	dropColumnIfExists(db, "api_keys", "rate_limit_rpm")
	dropColumnIfExists(db, "api_keys", "budget_limit_usd")
	dropColumnIfExists(db, "api_keys", "budget_period")
	// Migrate API keys from plaintext storage to SHA-256 hash. Pre-hash
	// databases hold the full sk-... string in the "key" column; the
	// entity now maps KeyHash to that same column, so the migration just
	// replaces the plaintext with its hash in place.
	backfillKeyHashes(db)
	return db, nil
}

// Close closes the underlying *sql.DB managed by GORM.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// backfillProviderConfigs creates ProviderConfig rows for every distinct
// provider_id found in connections that doesn't yet have a ProviderConfig
// row. This is a one-time migration that runs after AutoMigrate. Existing
// providers get default load_balance = "failover".
func backfillProviderConfigs(db *gorm.DB) {
	var providerIDs []string
	db.Model(&domain.Connection{}).Distinct("provider_id").Pluck("provider_id", &providerIDs)
	for _, pid := range providerIDs {
		var count int64
		db.Model(&domain.ProviderConfig{}).Where("id = ?", pid).Count(&count)
		if count == 0 {
			db.Create(&domain.ProviderConfig{
				ID:          pid,
				Name:        pid,
				LoadBalance: "failover",
			})
		}
	}
}

// dropColumnIfExists removes a column from a table if it exists. Used to
// clean up columns that were removed from the Go struct — GORM's
// AutoMigrate adds new columns but never drops old ones.
func dropColumnIfExists(db *gorm.DB, table, column string) {
	if db.Dialector.Name() == "postgres" {
		db.Exec("ALTER TABLE " + table + " DROP COLUMN IF EXISTS " + column)
		return
	}
	// SQLite: PRAGMA table_info returns rows with {cid, name, type, ...}.
	// We only care about the "name" field (index 1).
	type pragmaRow struct {
		CID  int    `gorm:"column:cid"`
		Name string `gorm:"column:name"`
	}
	var rows []pragmaRow
	db.Raw("PRAGMA table_info(" + table + ")").Scan(&rows)
	for _, r := range rows {
		if r.Name == column {
			db.Exec("ALTER TABLE " + table + " DROP COLUMN " + column)
			return
		}
	}
}

// backfillKeyHashes migrates existing API keys from plaintext to SHA-256
// hashes. Pre-hash databases hold the full sk-... string in the "key"
// column; rows still in plaintext are detected by the sk- prefix and
// replaced with their hash in place. Runs once after AutoMigrate; no-ops
// once every row holds a hash (SHA-256 hex, no sk- prefix).
func backfillKeyHashes(db *gorm.DB) {
	type row struct {
		ID  string `gorm:"column:id"`
		Key string `gorm:"column:key"`
	}
	var oldRows []row
	db.Table("api_keys").Select("id, key").Where("key LIKE 'sk-%'").Scan(&oldRows)
	for _, r := range oldRows {
		db.Exec("UPDATE api_keys SET key = ? WHERE id = ?", apikey.HashKey(r.Key), r.ID)
	}
}
