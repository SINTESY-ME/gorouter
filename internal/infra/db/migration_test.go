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

func sqliteOpen(dsn string) gorm.Dialector {
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	return sqlite.Open(dsn)
}

func TestMigrationPlaintextKeyToHash(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migrate.db")

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

	ctx := context.Background()
	db, err := Open(ctx, "sqlite", dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer Close(db)

	repo := NewApiKeyRepo(db)
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

	var stored string
	db.Table("api_keys").Select("key").Where("id = ?", "key-1").Scan(&stored)
	if stored == plaintext {
		t.Fatal("plaintext still stored after migration")
	}
	if stored != apikey.HashKey(plaintext) {
		t.Fatalf("stored = %q, want %q", stored, apikey.HashKey(plaintext))
	}

	if err := repo.Create(ctx, &domain.ApiKey{ID: "key-2", KeyHash: apikey.HashKey("sk-fresh"), Name: "new"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	k2, err := repo.GetByKey(ctx, "sk-fresh")
	if err != nil || k2 == nil {
		t.Fatalf("fresh key lookup failed: %v (nil=%v)", err, k2 == nil)
	}
}

func TestMigrationUserIdentity(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "users.db")
	gdb, err := gorm.Open(sqliteOpen(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL,
		permissions TEXT,
		created_at DATETIME,
		updated_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Exec(`INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, ?, ?)`, "user-1", "legacy-admin", "hash", "admin").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := gdb.DB()
	sqlDB.Close()

	db, err := Open(context.Background(), "sqlite", dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer Close(db)

	user, err := NewUserRepo(db).Get(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if user.Name != "legacy-admin" {
		t.Fatalf("Name = %q, want legacy-admin", user.Name)
	}
	if user.Email != "legacy-user-1@localhost" {
		t.Fatalf("Email = %q, want legacy-user-1@localhost", user.Email)
	}
	if !db.Migrator().HasIndex(&domain.User{}, "idx_users_email") {
		t.Fatal("email index was not created")
	}
}
