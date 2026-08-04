package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// scopedService builds a DB-backed set of repos with two users (admin +
// member) and returns helpers for the scope tests.
func scopedService(t *testing.T) (context.Context, *domain.UserScope, *domain.UserScope, *ApiKeyRepo, *ComboRepo, *ProviderConfigRepo) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "scope.db")
	ctx := context.Background()
	gdb, err := Open(ctx, "sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Close(gdb) })

	keyRepo := NewApiKeyRepo(gdb)
	comboRepo := NewComboRepo(gdb)
	provRepo := NewProviderConfigRepo(gdb)

	admin := &domain.UserScope{UserID: "admin-1", Role: domain.RoleAdmin}
	member := &domain.UserScope{UserID: "member-1", Role: domain.RoleMember}

	// Seed resources: one owned by member, one owned by admin.
	if err := provRepo.Create(ctx, &domain.ProviderConfig{ID: "member-prov", Name: "member provider", BaseURL: "https://x", CreatedBy: "member-1"}); err != nil {
		t.Fatal(err)
	}
	if err := provRepo.Create(ctx, &domain.ProviderConfig{ID: "admin-prov", Name: "admin provider", BaseURL: "https://y", CreatedBy: ""}); err != nil {
		t.Fatal(err)
	}
	if err := comboRepo.Create(ctx, &domain.Combo{ID: "c1", Name: "member-combo", Models: []string{"openai/gpt-4"}, CreatedBy: "member-1"}); err != nil {
		t.Fatal(err)
	}
	if err := comboRepo.Create(ctx, &domain.Combo{ID: "c2", Name: "admin-combo", Models: []string{"openai/gpt-4"}, CreatedBy: ""}); err != nil {
		t.Fatal(err)
	}
	if err := keyRepo.Create(ctx, &domain.ApiKey{ID: "k1", KeyHash: "a", Name: "member-key", CreatedBy: "member-1"}); err != nil {
		t.Fatal(err)
	}
	if err := keyRepo.Create(ctx, &domain.ApiKey{ID: "k2", KeyHash: "b", Name: "admin-key", CreatedBy: ""}); err != nil {
		t.Fatal(err)
	}

	return ctx, admin, member, keyRepo, comboRepo, provRepo
}

func TestScopedProviders(t *testing.T) {
	ctx, admin, member, _, _, provRepo := scopedService(t)

	// Admin sees everything.
	adminCtx := domain.WithUserScope(ctx, admin)
	ps, err := provRepo.List(adminCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("admin providers = %d, want 2", len(ps))
	}

	// Member sees only their own (no grants).
	memberCtx := domain.WithUserScope(ctx, member)
	ps, err = provRepo.List(memberCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].ID != "member-prov" {
		t.Fatalf("member providers = %+v, want only member-prov", ps)
	}

	// Internal (no scope) sees everything like admin.
	ps, err = provRepo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("internal providers = %d, want 2", len(ps))
	}
}

func TestScopedCombosAndKeys(t *testing.T) {
	ctx, admin, member, keyRepo, comboRepo, _ := scopedService(t)

	adminCtx := domain.WithUserScope(ctx, admin)
	memberCtx := domain.WithUserScope(ctx, member)

	cs, err := comboRepo.List(adminCtx)
	if err != nil || len(cs) != 2 {
		t.Fatalf("admin combos = %d (err %v), want 2", len(cs), err)
	}
	cs, err = comboRepo.List(memberCtx)
	if err != nil || len(cs) != 1 || cs[0].Name != "member-combo" {
		t.Fatalf("member combos = %+v (err %v), want only member-combo", cs, err)
	}

	ks, err := keyRepo.List(adminCtx)
	if err != nil || len(ks) != 2 {
		t.Fatalf("admin keys = %d (err %v), want 2", len(ks), err)
	}
	ks, err = keyRepo.List(memberCtx)
	if err != nil || len(ks) != 1 || ks[0].Name != "member-key" {
		t.Fatalf("member keys = %+v (err %v), want only member-key", ks, err)
	}
}

func TestScopedProviderGrant(t *testing.T) {
	ctx, _, member, _, _, provRepo := scopedService(t)
	accessRepo := NewUserAccessRepo(provRepo.db)
	if err := accessRepo.Set(ctx, domain.UserAccessProvider, "member-1", []string{"admin-prov"}); err != nil {
		t.Fatal(err)
	}
	memberCtx := domain.WithUserScope(ctx, member)
	ps, err := provRepo.List(memberCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("member providers with grant = %d, want 2", len(ps))
	}
}
