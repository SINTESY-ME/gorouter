package app

import (
	"context"
	"testing"

	"github.com/jhon/gorouter/internal/domain"
)

// fakeAccessRepo is an in-memory domain.UserAccessRepo.
type fakeAccessRepo struct {
	grants map[string][]string // "kind:userID" -> resourceIDs
}

func newFakeAccessRepo() *fakeAccessRepo {
	return &fakeAccessRepo{grants: map[string][]string{}}
}

func (r *fakeAccessRepo) List(_ context.Context, kind domain.UserAccessKind, userID string) ([]string, error) {
	return r.grants[string(kind)+":"+userID], nil
}
func (r *fakeAccessRepo) Set(_ context.Context, kind domain.UserAccessKind, userID string, ids []string) error {
	r.grants[string(kind)+":"+userID] = ids
	return nil
}
func (r *fakeAccessRepo) DeleteAll(_ context.Context, userID string) error {
	for k := range r.grants {
		if len(k) > len(userID) && k[len(k)-len(userID):] == userID {
			delete(r.grants, k)
		}
	}
	return nil
}

func TestUserServiceCreateUpdateDelete(t *testing.T) {
	users := &fakeUserRepo{}
	access := newFakeAccessRepo()
	svc := &UserService{Users: users, Access: access}
	ctx := context.Background()

	admin, err := svc.CreateUser(ctx, "Admin", "admin@example.com", "pw1", domain.RoleAdmin, domain.UserPermissions{})
	if err != nil {
		t.Fatal(err)
	}
	if !admin.IsAdmin() {
		t.Fatal("created user should be admin")
	}
	if admin.PasswordHash == "" || admin.PasswordHash == "pw1" {
		t.Fatal("password must be hashed")
	}

	member, err := svc.CreateUser(ctx, "Bob", "bob@example.com", "pw2", domain.RoleMember, domain.UserPermissions{CanCreateCombos: true})
	if err != nil {
		t.Fatal(err)
	}
	if member.IsAdmin() {
		t.Fatal("member should not be admin")
	}

	// Update member permissions.
	updated, err := svc.UpdateUser(ctx, member.ID, "", "", "", domain.RoleMember, &domain.UserPermissions{CanManageCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Permissions.CanManageCache {
		t.Fatal("permissions not updated")
	}

	// Cannot delete the last admin.
	if err := svc.DeleteUser(ctx, admin.ID); err == nil {
		t.Fatal("expected error deleting last admin")
	}

	// Delete member ok.
	if err := svc.DeleteUser(ctx, member.ID); err != nil {
		t.Fatalf("delete member: %v", err)
	}
}

func TestUserServiceAccess(t *testing.T) {
	users := &fakeUserRepo{}
	access := newFakeAccessRepo()
	svc := &UserService{Users: users, Access: access}
	ctx := context.Background()

	member, err := svc.CreateUser(ctx, "Bob", "bob@example.com", "pw", domain.RoleMember, domain.UserPermissions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAccess(ctx, domain.UserAccessModel, member.ID, []string{"openai/gpt-4o", "anthropic/claude-3"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadUserGrants(ctx, member); err != nil {
		t.Fatal(err)
	}
	if len(member.AllowedModels) != 2 {
		t.Fatalf("allowed models = %v, want 2", member.AllowedModels)
	}
	// Invalid kind rejected.
	if err := svc.SetAccess(ctx, "bogus", member.ID, []string{"x"}); err == nil {
		t.Fatal("expected error for invalid access kind")
	}
}

func TestUserServiceLoadGrantsAdminNoop(t *testing.T) {
	users := &fakeUserRepo{}
	svc := &UserService{Users: users, Access: newFakeAccessRepo()}
	admin, err := svc.CreateUser(ctx(), "Admin", "admin@example.com", "pw", domain.RoleAdmin, domain.UserPermissions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadUserGrants(ctx(), admin); err != nil {
		t.Fatal(err)
	}
	if len(admin.AllowedModels) != 0 {
		t.Fatal("admin should have no grant filtering")
	}
}

func ctx() context.Context { return context.Background() }
