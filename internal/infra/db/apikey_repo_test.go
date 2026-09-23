package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
	"github.com/jhon/gorouter/internal/infra/apikey"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openApiKeyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "keys.db")
	gdb, err := gorm.Open(sqliteOpen(dsn), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.AutoMigrate(&domain.ApiKey{}); err != nil {
		t.Fatal(err)
	}
	return gdb
}

// Editing a key must land: limits and the model/combo access list are what the
// edit dialog is for, and an update that only wrote name/is_active looked
// successful while saving nothing.
func TestApiKeyRepoUpdatePersistsLimitsAndAllowedModels(t *testing.T) {
	gdb := openApiKeyTestDB(t)
	repo := NewApiKeyRepo(gdb)
	ctx := context.Background()

	k := &domain.ApiKey{
		ID:       "key-1",
		KeyHash:  apikey.HashKey("sk-abc"),
		Name:     "before",
		IsActive: true,
	}
	if err := repo.Create(ctx, k); err != nil {
		t.Fatal(err)
	}

	k.Name = "after"
	k.IsActive = false
	k.Limits = []domain.KeyLimit{{ID: "l1", Kind: domain.KeyLimitRate, Max: 25, Duration: "1d"}}
	k.AllowedModels = []string{"openai/gpt-4o", "sintesy-combo"}
	if err := repo.Update(ctx, k); err != nil {
		t.Fatal(err)
	}

	stored, err := repo.Get(ctx, k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil {
		t.Fatal("key disappeared after update")
	}
	if stored.Name != "after" || stored.IsActive {
		t.Fatalf("name/is_active not persisted: %+v", stored)
	}
	if len(stored.Limits) != 1 || stored.Limits[0].Max != 25 {
		t.Fatalf("limits not persisted: %+v", stored.Limits)
	}
	// A combo name is a legal entry here: the router matches allowed models
	// against combo names as well as model ids.
	if len(stored.AllowedModels) != 2 || stored.AllowedModels[1] != "sintesy-combo" {
		t.Fatalf("allowed models not persisted: %+v", stored.AllowedModels)
	}

	// Clearing them must stick too — the old struct-based Updates skipped
	// zero values, so "remove all limits" was a no-op.
	stored.Limits = nil
	stored.AllowedModels = nil
	if err := repo.Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	cleared, err := repo.Get(ctx, k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.Limits) != 0 || len(cleared.AllowedModels) != 0 {
		t.Fatalf("clearing limits/models did not persist: %+v", cleared)
	}
}

// Rotation has to move the hash and the sealed plaintext together: a hash
// written without its cipher leaves a key nobody can ever copy again.
func TestApiKeyRepoUpdateSecretSwapsLookupAndCipher(t *testing.T) {
	gdb := openApiKeyTestDB(t)
	repo := NewApiKeyRepo(gdb)
	ctx := context.Background()

	k := &domain.ApiKey{ID: "key-2", KeyHash: apikey.HashKey("sk-old"), Name: "rotating", IsActive: true}
	if err := repo.Create(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateSecret(ctx, k.ID, apikey.HashKey("sk-new"), "sealed-blob"); err != nil {
		t.Fatal(err)
	}

	byOldKey, err := repo.GetByKey(ctx, "sk-old")
	if err != nil {
		t.Fatal(err)
	}
	if byOldKey != nil {
		t.Fatal("the rotated-away key still authenticates")
	}
	byNewKey, err := repo.GetByKey(ctx, "sk-new")
	if err != nil {
		t.Fatal(err)
	}
	if byNewKey == nil {
		t.Fatal("the new key does not authenticate")
	}
	if byNewKey.KeyCipher != "sealed-blob" {
		t.Fatalf("cipher not stored with the new hash: %q", byNewKey.KeyCipher)
	}
	if err := repo.UpdateSecret(ctx, "missing", "hash", "cipher"); err == nil {
		t.Fatal("rotating an unknown key must fail")
	}
}
