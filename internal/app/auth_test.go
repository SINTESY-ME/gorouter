package app

import (
	"context"
	"strings"
	"testing"
)

// fakeSettingRepo is a minimal in-memory SettingRepo for auth tests.
type fakeSettingRepo struct {
	vals map[string]string
}

func newFakeSettingRepo() *fakeSettingRepo {
	return &fakeSettingRepo{vals: map[string]string{}}
}

func (f *fakeSettingRepo) Get(_ context.Context, key string) (string, error) {
	return f.vals[key], nil
}

func (f *fakeSettingRepo) Set(_ context.Context, key, value string) error {
	f.vals[key] = value
	return nil
}

func (f *fakeSettingRepo) Has(_ context.Context, key string) (bool, error) {
	_, ok := f.vals[key]
	return ok, nil
}

func TestHashPasswordBcrypt(t *testing.T) {
	h := HashPassword("s3cret!")
	if !strings.HasPrefix(h, "$2") {
		t.Fatalf("HashPassword = %q, want bcrypt prefix $2", h)
	}
	if !ComparePassword("s3cret!", h) {
		t.Error("ComparePassword failed for correct password")
	}
	if ComparePassword("wrong", h) {
		t.Error("ComparePassword accepted wrong password")
	}
	if HashPassword("s3cret!") == h {
		t.Error("bcrypt salt should produce different hashes for same password")
	}
}

func TestComparePasswordLegacy(t *testing.T) {
	// A legacy unsalted sha256 hash must still validate.
	legacy := "2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b" // sha256("secret")
	if !ComparePassword("secret", legacy) {
		t.Error("ComparePassword rejected valid legacy sha256 hash")
	}
	if ComparePassword("wrong", legacy) {
		t.Error("ComparePassword accepted wrong password against legacy hash")
	}
}

func TestValidateTokenMigratesLegacyHash(t *testing.T) {
	repo := newFakeSettingRepo()
	// Seed with a legacy unsalted sha256 hash.
	repo.Set(context.Background(), SettingKeyDashboardPassword, "2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b")
	auth := &AuthService{Repo: repo}

	ok, err := auth.ValidateToken(context.Background(), "secret")
	if err != nil {
		t.Fatalf("ValidateToken error: %v", err)
	}
	if !ok {
		t.Fatal("ValidateToken rejected valid legacy password")
	}

	// The stored hash must now be bcrypt.
	stored, _ := repo.Get(context.Background(), SettingKeyDashboardPassword)
	if !strings.HasPrefix(stored, "$2") {
		t.Fatalf("after migration stored hash = %q, want bcrypt", stored)
	}
	// And a wrong password must still fail against the migrated hash.
	if ok, _ := auth.ValidateToken(context.Background(), "wrong"); ok {
		t.Fatal("ValidateToken accepted wrong password after migration")
	}
}

func TestValidateTokenRejectsBadHash(t *testing.T) {
	repo := newFakeSettingRepo()
	repo.Set(context.Background(), SettingKeyDashboardPassword, "not-a-real-hash")
	auth := &AuthService{Repo: repo}
	if ok, _ := auth.ValidateToken(context.Background(), "anything"); ok {
		t.Fatal("ValidateToken accepted password against malformed stored hash")
	}
}

func TestAuthEnvTokenTakesPriority(t *testing.T) {
	repo := newFakeSettingRepo()
	auth := &AuthService{EnvToken: "env-token", Repo: repo}
	if ok, _ := auth.ValidateToken(context.Background(), "env-token"); !ok {
		t.Fatal("ValidateToken rejected env token")
	}
	if ok, _ := auth.ValidateToken(context.Background(), "other"); ok {
		t.Fatal("ValidateToken accepted non-env token")
	}
}

func TestSetupStoresBcrypt(t *testing.T) {
	repo := newFakeSettingRepo()
	auth := &AuthService{Repo: repo}
	if err := auth.Setup(context.Background(), "pw"); err != nil {
		t.Fatalf("Setup error: %v", err)
	}
	stored, _ := repo.Get(context.Background(), SettingKeyDashboardPassword)
	if !strings.HasPrefix(stored, "$2") {
		t.Fatalf("stored hash = %q, want bcrypt", stored)
	}
	if ok, _ := auth.Login(context.Background(), "pw"); !ok {
		t.Fatal("Login rejected the setup password")
	}
	// Second setup must fail.
	if err := auth.Setup(context.Background(), "another"); err != ErrAuthAlreadyConfigured {
		t.Fatalf("second Setup err = %v, want ErrAuthAlreadyConfigured", err)
	}
}
