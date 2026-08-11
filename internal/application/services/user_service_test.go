package services

import (
	"context"
	"errors"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// fakeUserRoleRepo is an in-memory ports.UserRoleRepository so the service is
// tested with no database or network.
type fakeUserRoleRepo struct {
	roles     []*domain.UserRole
	assignErr error
	listErr   error
	assigned  []*domain.UserRole
}

func (f *fakeUserRoleRepo) Assign(ctx context.Context, role *domain.UserRole) error {
	if f.assignErr != nil {
		return f.assignErr
	}
	f.assigned = append(f.assigned, role)
	return nil
}

func (f *fakeUserRoleRepo) ListByUser(ctx context.Context, tenantID, userID string) ([]*domain.UserRole, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var owned []*domain.UserRole
	for _, r := range f.roles {
		if r.UserID == userID {
			owned = append(owned, r)
		}
	}
	return owned, nil
}

func newUserServiceTestHarness(users *fakeUserRepo, roles *fakeUserRoleRepo, sink *recordingSink) ports.UserService {
	if users == nil {
		users = &fakeUserRepo{}
	}
	if roles == nil {
		roles = &fakeUserRoleRepo{}
	}
	if sink == nil {
		sink = &recordingSink{}
	}
	return NewUserService(users, roles, &fakeHasher{}, sink)
}

// TestUserServiceCreateHappyPath proves CreateUser builds a user with the
// hasher's PHC hash (never the plaintext), assigns the initial role and emits
// user.create (info) with the actor. No password material is stored.
func TestUserServiceCreateHappyPath(t *testing.T) {
	users := &fakeUserRepo{}
	roles := &fakeUserRoleRepo{}
	sink := &recordingSink{}
	svc := newUserServiceTestHarness(users, roles, sink)

	user, err := svc.CreateUser(context.Background(), "tenant-1", "actor-1", ports.UserInput{
		Email:    "new@esperanza.coop",
		Password: "s3cret-password",
		FullName: "Nueva Productora",
		Role:     domain.RoleProducer,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if user.TenantID != "tenant-1" || user.ID == "" {
		t.Fatalf("created user = %+v, want tenant tenant-1 and a generated id", user)
	}
	if user.PasswordHash != "hash" {
		t.Fatalf("stored PasswordHash = %q, want the hasher output (fake hasher returns %q)", user.PasswordHash, "hash")
	}
	if user.PasswordHash == "s3cret-password" {
		t.Fatal("stored PasswordHash must never equal the plaintext password")
	}
	if user.Email != "new@esperanza.coop" || user.FullName != "Nueva Productora" || !user.IsActive {
		t.Fatalf("created user fields = %+v, want email/full_name set and is_active=true", user)
	}

	if len(users.created) != 1 || users.created[0] != user {
		t.Fatal("the user repository must have received exactly the created user")
	}
	if len(roles.assigned) != 1 {
		t.Fatalf("assigned roles = %+v, want exactly one", roles.assigned)
	}
	assigned := roles.assigned[0]
	if assigned.UserID != user.ID || assigned.RoleCode != domain.RoleProducer || assigned.TenantID != "tenant-1" {
		t.Fatalf("assigned role = %+v, want user=%s producer in tenant-1", assigned, user.ID)
	}

	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (user.create)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Action != "user.create" || ev.Severity != domain.SeverityInfo || ev.ActorID != "actor-1" || ev.TenantID != "tenant-1" {
		t.Fatalf("event = %+v, want user.create/info/actor-1/tenant-1", ev)
	}
}

// TestUserServiceCreateRejectsInvalidRoleBeforeWrite proves an unknown role
// code surfaces as ErrInvalidInput BEFORE any write: no user row, no role
// assignment, no audit event (R10).
func TestUserServiceCreateRejectsInvalidRoleBeforeWrite(t *testing.T) {
	users := &fakeUserRepo{}
	roles := &fakeUserRoleRepo{}
	sink := &recordingSink{}
	svc := newUserServiceTestHarness(users, roles, sink)

	_, err := svc.CreateUser(context.Background(), "tenant-1", "actor-1", ports.UserInput{
		Email:    "new@esperanza.coop",
		Password: "s3cret",
		FullName: "Nueva Productora",
		Role:     "superuser",
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("CreateUser invalid role error = %v, want ErrInvalidInput", err)
	}
	if len(users.created) != 0 {
		t.Fatal("no user row may be created when the role is invalid")
	}
	if len(roles.assigned) != 0 {
		t.Fatal("no role may be assigned when the role is invalid")
	}
	if len(sink.events) != 0 {
		t.Fatal("no audit event may be emitted when the role is invalid")
	}
}

// TestUserServiceCreateDuplicateEmailConflict proves a 23505-style duplicate
// (same or cross tenant — the repo owns the global unique index) surfaces as
// domain.ErrConflict and the role is NOT assigned on a failed user insert
// (R11).
func TestUserServiceCreateDuplicateEmailConflict(t *testing.T) {
	users := &fakeUserRepo{err: domain.ErrConflict}
	roles := &fakeUserRoleRepo{}
	sink := &recordingSink{}
	svc := newUserServiceTestHarness(users, roles, sink)

	_, err := svc.CreateUser(context.Background(), "tenant-1", "actor-1", ports.UserInput{
		Email:    "dup@esperanza.coop",
		Password: "s3cret",
		FullName: "Duplicada",
		Role:     domain.RoleProducer,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("CreateUser duplicate email error = %v, want ErrConflict", err)
	}
	if len(roles.assigned) != 0 {
		t.Fatal("no role may be assigned when the user insert failed")
	}
	if len(sink.events) != 0 {
		t.Fatal("no audit event may be emitted when the user insert failed")
	}
}

// TestUserServiceCreateRejectsInvalidUserInput proves domain.User.IsValid
// failures (empty email) surface as ErrInvalidInput and nothing is written.
func TestUserServiceCreateRejectsInvalidUserInput(t *testing.T) {
	users := &fakeUserRepo{}
	roles := &fakeUserRoleRepo{}
	svc := newUserServiceTestHarness(users, roles, nil)

	_, err := svc.CreateUser(context.Background(), "tenant-1", "actor-1", ports.UserInput{
		Email:    "",
		Password: "s3cret",
		FullName: "Sin Email",
		Role:     domain.RoleProducer,
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("CreateUser invalid user error = %v, want ErrInvalidInput", err)
	}
	if len(users.created) != 0 {
		t.Fatal("no user row may be created when the user is invalid")
	}
	if len(roles.assigned) != 0 {
		t.Fatal("no role may be assigned when the user is invalid")
	}
}

// TestUserServiceCreateRequiresTenant proves CreateUser rejects an empty
// tenant with ErrTenantRequired before any validation or write.
func TestUserServiceCreateRequiresTenant(t *testing.T) {
	users := &fakeUserRepo{}
	roles := &fakeUserRoleRepo{}
	sink := &recordingSink{}
	svc := newUserServiceTestHarness(users, roles, sink)

	_, err := svc.CreateUser(context.Background(), "", "actor-1", ports.UserInput{
		Email: "new@esperanza.coop", Password: "s3cret", FullName: "Nueva", Role: domain.RoleProducer,
	})
	if !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("CreateUser empty tenant error = %v, want ErrTenantRequired", err)
	}
	if len(users.created) != 0 || len(roles.assigned) != 0 || len(sink.events) != 0 {
		t.Fatal("no write or audit may happen when the tenant is missing")
	}
}

// TestUserServiceListUsers proves ListUsers passes the tenant scoping key
// through to the repository.
func TestUserServiceListUsers(t *testing.T) {
	want := []*domain.User{
		{ID: "u-1", TenantID: "tenant-1", Email: "a@test.local", FullName: "A"},
		{ID: "u-2", TenantID: "tenant-1", Email: "b@test.local", FullName: "B"},
	}
	svc := newUserServiceTestHarness(&fakeUserRepo{list: want}, nil, nil)

	got, err := svc.ListUsers(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got) != 2 || got[0].ID != "u-1" || got[1].ID != "u-2" {
		t.Fatalf("ListUsers = %+v, want [u-1, u-2]", got)
	}

	if _, err := svc.ListUsers(context.Background(), ""); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("ListUsers empty tenant error = %v, want ErrTenantRequired", err)
	}
}

// TestUserServiceListUsersResolvesRoles proves every listed user carries its
// server-resolved role (R13 precedence: admin > agronomist > producer >
// auditor > hauler), roleless users stay "", and a membership lookup failure
// propagates — a role that cannot be resolved must never be silently dropped
// (fail closed).
func TestUserServiceListUsersResolvesRoles(t *testing.T) {
	users := &fakeUserRepo{list: []*domain.User{
		{ID: "u-admin", TenantID: "tenant-1", Email: "a@test.local", FullName: "A"},
		{ID: "u-roleless", TenantID: "tenant-1", Email: "b@test.local", FullName: "B"},
	}}
	roles := &fakeUserRoleRepo{roles: []*domain.UserRole{
		{UserID: "u-admin", RoleCode: domain.RoleProducer, TenantID: "tenant-1"},
		{UserID: "u-admin", RoleCode: domain.RoleAdmin, TenantID: "tenant-1"},
	}}
	svc := newUserServiceTestHarness(users, roles, nil)

	got, err := svc.ListUsers(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListUsers = %d users, want 2", len(got))
	}
	if got[0].Role != domain.RoleAdmin {
		t.Fatalf("u-admin role = %q, want %q (most privileged of producer+admin)", got[0].Role, domain.RoleAdmin)
	}
	if got[1].Role != "" {
		t.Fatalf("u-roleless role = %q, want empty", got[1].Role)
	}

	failing := &fakeUserRoleRepo{listErr: errors.New("boom")}
	svcFail := newUserServiceTestHarness(&fakeUserRepo{list: users.list}, failing, nil)
	if _, err := svcFail.ListUsers(context.Background(), "tenant-1"); err == nil {
		t.Fatal("ListUsers with failing membership lookup: err = nil, want failure (fail closed)")
	}
}

// TestUserServiceUpdateUser proves UpdateUser applies the full_name and
// is_active toggle (full-row replace semantics: the fetched user's other
// fields — email, hash — are preserved) and returns the updated user.
func TestUserServiceUpdateUser(t *testing.T) {
	existing := &domain.User{
		ID: "u-1", TenantID: "tenant-1", Email: "a@test.local",
		PasswordHash: "hash", FullName: "Antes", IsActive: true,
	}
	users := &fakeUserRepo{user: existing}
	svc := newUserServiceTestHarness(users, nil, nil)

	updated, err := svc.UpdateUser(context.Background(), "tenant-1", "actor-1", "u-1", ports.UpdateUserInput{
		FullName: "Después",
		IsActive: false,
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.FullName != "Después" || updated.IsActive {
		t.Fatalf("updated user = %+v, want full_name=Después is_active=false", updated)
	}
	if updated.Email != "a@test.local" || updated.PasswordHash != "hash" {
		t.Fatalf("updated user must preserve email and hash (full-row replace), got %+v", updated)
	}
	if len(users.updated) != 1 || users.updated[0].FullName != "Después" || users.updated[0].IsActive {
		t.Fatalf("repo updated = %+v, want exactly the toggled user", users.updated)
	}
}

// TestUserServiceUpdateUserNotFound proves a missing user propagates
// domain.ErrNotFound and the repository Update is never called.
func TestUserServiceUpdateUserNotFound(t *testing.T) {
	users := &fakeUserRepo{err: domain.ErrNotFound}
	svc := newUserServiceTestHarness(users, nil, nil)

	_, err := svc.UpdateUser(context.Background(), "tenant-1", "actor-1", "u-missing", ports.UpdateUserInput{
		FullName: "Nadie", IsActive: true,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("UpdateUser missing error = %v, want ErrNotFound", err)
	}
	if len(users.updated) != 0 {
		t.Fatal("repo Update must not run when the user is missing")
	}
}

// TestUserServiceUpdateUserRequiresTenant proves UpdateUser rejects an empty
// tenant before any lookup.
func TestUserServiceUpdateUserRequiresTenant(t *testing.T) {
	users := &fakeUserRepo{}
	svc := newUserServiceTestHarness(users, nil, nil)

	_, err := svc.UpdateUser(context.Background(), "", "actor-1", "u-1", ports.UpdateUserInput{
		FullName: "Nadie", IsActive: true,
	})
	if !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("UpdateUser empty tenant error = %v, want ErrTenantRequired", err)
	}
	if len(users.updated) != 0 {
		t.Fatal("no repo call may happen when the tenant is missing")
	}
}
