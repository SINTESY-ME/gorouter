package app

import (
	"context"
	"strings"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
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

// fakeUserRepo is an in-memory domain.UserRepo.
type fakeUserRepo struct {
	users []domain.User
}

func (r *fakeUserRepo) List(_ context.Context) ([]domain.User, error) { return r.users, nil }
func (r *fakeUserRepo) Get(_ context.Context, id string) (*domain.User, error) {
	for i := range r.users {
		if r.users[i].ID == id {
			return &r.users[i], nil
		}
	}
	return nil, domain.ErrNotFound
}
func (r *fakeUserRepo) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	for i := range r.users {
		if r.users[i].Email == email {
			return &r.users[i], nil
		}
	}
	return nil, nil
}
func (r *fakeUserRepo) Create(_ context.Context, u *domain.User) error {
	r.users = append(r.users, *u)
	return nil
}
func (r *fakeUserRepo) Update(_ context.Context, u *domain.User) error {
	for i := range r.users {
		if r.users[i].ID == u.ID {
			r.users[i] = *u
		}
	}
	return nil
}
func (r *fakeUserRepo) Delete(_ context.Context, id string) error {
	for i := range r.users {
		if r.users[i].ID == id {
			r.users = append(r.users[:i], r.users[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

// fakeSessionRepo is an in-memory domain.SessionRepo.
type fakeSessionRepo struct {
	sessions []domain.Session
}

func (r *fakeSessionRepo) Create(_ context.Context, s *domain.Session) error {
	r.sessions = append(r.sessions, *s)
	return nil
}
func (r *fakeSessionRepo) GetByTokenHash(_ context.Context, tokenHash string) (*domain.Session, error) {
	for i := range r.sessions {
		if r.sessions[i].TokenHash == tokenHash {
			return &r.sessions[i], nil
		}
	}
	return nil, nil
}
func (r *fakeSessionRepo) DeleteByUser(_ context.Context, userID string) error {
	for i := range r.sessions {
		if r.sessions[i].UserID == userID {
			r.sessions = append(r.sessions[:i], r.sessions[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func newAuthHarness() (*AuthService, *fakeUserRepo, *fakeSessionRepo, *fakeSettingRepo) {
	users := &fakeUserRepo{}
	sessions := &fakeSessionRepo{}
	settings := newFakeSettingRepo()
	auth := &AuthService{Users: users, Sessions: sessions, Setting: settings}
	return auth, users, sessions, settings
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
	legacy := "2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b" // sha256("secret")
	if !ComparePassword("secret", legacy) {
		t.Error("ComparePassword rejected valid legacy sha256 hash")
	}
	if ComparePassword("wrong", legacy) {
		t.Error("ComparePassword accepted wrong password against legacy hash")
	}
}

func TestSetupCreatesAdminAndLogin(t *testing.T) {
	auth, _, sessions, _ := newAuthHarness()
	if err := auth.Setup(context.Background(), "Admin", "admin@example.com", "secret"); err != nil {
		t.Fatalf("Setup error: %v", err)
	}
	if !auth.IsConfiguredVal(context.Background()) {
		t.Fatal("IsConfigured should be true after setup")
	}
	sess, err := auth.Login(context.Background(), "admin@example.com", "secret")
	if err != nil {
		t.Fatalf("Login error: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("Login returned empty session token")
	}
	if len(sessions.sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions.sessions))
	}
	// Wrong password.
	if _, err := auth.Login(context.Background(), "admin@example.com", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password err = %v, want ErrInvalidCredentials", err)
	}
	// Second setup must fail.
	if err := auth.Setup(context.Background(), "Other", "other@example.com", "y"); err != ErrAuthAlreadyConfigured {
		t.Fatalf("second Setup err = %v, want ErrAuthAlreadyConfigured", err)
	}
}

// IsConfiguredVal is a test helper wrapping IsConfigured.
func (a *AuthService) IsConfiguredVal(ctx context.Context) bool {
	v, _ := a.IsConfigured(ctx)
	return v
}

func TestValidateSessionRoundTrip(t *testing.T) {
	auth, _, _, _ := newAuthHarness()
	if err := auth.Setup(context.Background(), "Admin", "admin@example.com", "secret"); err != nil {
		t.Fatal(err)
	}
	sess, err := auth.Login(context.Background(), "admin@example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.ValidateSession(context.Background(), sess.Token)
	if err != nil {
		t.Fatalf("ValidateSession error: %v", err)
	}
	if user == nil || user.Email != "admin@example.com" {
		t.Fatalf("ValidateSession = %+v, want admin user", user)
	}
	// Invalid token → nil.
	if u, _ := auth.ValidateSession(context.Background(), "bogus"); u != nil {
		t.Fatal("ValidateSession accepted bogus token")
	}
}

func TestEnvTokenMaster(t *testing.T) {
	auth, users, _, _ := newAuthHarness()
	auth.EnvToken = "env-token"
	sess, err := auth.Login(context.Background(), "admin@localhost", "env-token")
	if err != nil {
		t.Fatalf("env-token Login error: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("env-token Login returned empty session")
	}
	if len(users.users) != 1 {
		t.Fatalf("users = %d, want 1 (lazily created admin)", len(users.users))
	}
}

func TestLegacyPasswordMigration(t *testing.T) {
	auth, users, _, settings := newAuthHarness()
	settings.Set(context.Background(), SettingKeyDashboardPassword, "2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b")
	sess, err := auth.Login(context.Background(), "admin@localhost", "secret")
	if err != nil {
		t.Fatalf("legacy Login error: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("legacy Login returned empty session")
	}
	if len(users.users) != 1 {
		t.Fatalf("users = %d, want 1 (migrated admin)", len(users.users))
	}
	// Legacy setting cleared.
	v, _ := settings.Get(context.Background(), SettingKeyDashboardPassword)
	if v != "" {
		t.Fatalf("legacy setting not cleared, got %q", v)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	auth, _, sessions, _ := newAuthHarness()
	if err := auth.Setup(context.Background(), "Admin", "admin@example.com", "secret"); err != nil {
		t.Fatal(err)
	}
	sess, err := auth.Login(context.Background(), "admin@example.com", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Logout(context.Background(), sess.UserID); err != nil {
		t.Fatalf("Logout error: %v", err)
	}
	if u, _ := auth.ValidateSession(context.Background(), sess.Token); u != nil {
		t.Fatal("ValidateSession succeeded after logout")
	}
	if len(sessions.sessions) != 0 {
		t.Fatalf("sessions = %d, want 0 after logout", len(sessions.sessions))
	}
}
