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
		Logger:        logger.Default.LogMode(logger.Warn),
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
	if err := db.AutoMigrate(&domain.Connection{}, &domain.Combo{}, &domain.ApiKey{}, &domain.UsageEntry{}, &domain.ModelEntry{}, &domain.Setting{}); err != nil {
		_ = Close(db)
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}
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