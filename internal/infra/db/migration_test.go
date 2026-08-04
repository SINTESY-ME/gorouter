package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/apikey"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sqliteOpen mirrors Open()'s SQLite DSN handling (file: URI prefix).
func sqliteOpen(dsn string) gorm.Dialector {
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	return sqlite.Open(dsn)
}

// TestMigrationPlaintextKeyToHash simulates an old database that still has
// the plaintext "key" column (pre-hash schema), then runs Open() which
// applies AutoMigrate + migrations. It verifies:
//  1. key_hash is backfilled from the plaintext column
//  2. the old "key" column and its unique index are dropped
//  3. GetByKey finds the key by hashing the incoming plaintext
func TestMigrationPlaintextKeyToHash(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migrate.db")

	// Phase 1: create an OLD-schema database with a plaintext key column.
	gdb, err := gorm.Open(sqliteOpen(dsn), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.Exec(`CREATE TABLE api_keys (
		id TEXT PRIMARY KEY,
		key TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '',
		is_active BOOLEAN NOT NULL DEFAULT true,
		limits TEXT,
		allowed_models TEXT,
		created_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	const plaintext = "sk-test123456789012345678-ab12cd34"
	if err := gdb.Exec(`INSERT INTO api_keys (id, key, name, is_active, created_at) VALUES (?, ?, ?, true, datetime('now'))`,
		"key-1", plaintext, "legacy").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := gdb.DB()
	sqlDB.Close()

	// Phase 2: Open() runs AutoMigrate + backfill + column/index drop.
	ctx := context.Background()
	db, err := Open(ctx, "sqlite", dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer Close(db)

	repo := NewApiKeyRepo(db)

	// Backfill worked: lookup by hashing the plaintext finds the key.
	k, err := repo.GetByKey(ctx, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if k == nil {
		t.Fatal("GetByKey returned nil after migration")
	}
	if k.KeyHash != apikey.HashKey(plaintext) {
		t.Fatalf("KeyHash = %q, want %q", k.KeyHash, apikey.HashKey(plaintext))
	}
	if k.Name != "legacy" {
		t.Fatalf("Name = %q, want legacy", k.Name)
	}

	// The stored value is now the hash, not the plaintext.
	var stored string
	db.Table("api_keys").Select("key").Where("id = ?", "key-1").Scan(&stored)
	if stored == plaintext {
		t.Fatal("plaintext still stored after migration")
	}
	if stored != apikey.HashKey(plaintext) {
		t.Fatalf("stored = %q, want %q", stored, apikey.HashKey(plaintext))
	}

	// A fresh key persists only its hash.
	if err := repo.Create(ctx, &domain.ApiKey{ID: "key-2", KeyHash: apikey.HashKey("sk-fresh"), Name: "new"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	k2, err := repo.GetByKey(ctx, "sk-fresh")
	if err != nil || k2 == nil {
		t.Fatalf("fresh key lookup failed: %v (nil=%v)", err, k2 == nil)
	}
}
