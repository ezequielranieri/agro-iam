package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// createTestUser inserts a user owned by the given tenant through the
// repository, which binds the RLS context from user.TenantID.
func createTestUser(ctx context.Context, t *testing.T, repo *UserRepo, tenantID, email string) *domain.User {
	t.Helper()
	user := &domain.User{
		ID:           newUUID(t),
		TenantID:     tenantID,
		Email:        email,
		PasswordHash: "not-a-real-hash-for-tests",
		FullName:     "Test User",
		IsActive:     true,
	}
	if err := repo.Create(ctx, user); err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return user
}

// TestUserRoleRepoAssignAndListByUser proves Assign inserts a role membership
// inside the tenant context (WithTenant) and ListByUser returns it — and that
// RLS keeps every tenant's memberships invisible to the others.
func TestUserRoleRepoAssignAndListByUser(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	userRepo := NewUserRepo(testPool)
	userRoleRepo := NewUserRoleRepo(testPool)

	tenantA := createTestTenant(ctx, t, tenantRepo, "Coop A")
	tenantB := createTestTenant(ctx, t, tenantRepo, "Coop B")
	userA := createTestUser(ctx, t, userRepo, tenantA.ID, "producer-a@test.local")
	userB := createTestUser(ctx, t, userRepo, tenantB.ID, "producer-b@test.local")

	if err := userRoleRepo.Assign(ctx, &domain.UserRole{UserID: userA.ID, RoleCode: domain.RoleProducer, TenantID: tenantA.ID}); err != nil {
		t.Fatalf("Assign(producer, A): %v", err)
	}
	if err := userRoleRepo.Assign(ctx, &domain.UserRole{UserID: userB.ID, RoleCode: domain.RoleAdmin, TenantID: tenantB.ID}); err != nil {
		t.Fatalf("Assign(admin, B): %v", err)
	}

	// The owning tenant lists exactly its own memberships.
	rolesA, err := userRoleRepo.ListByUser(ctx, tenantA.ID, userA.ID)
	if err != nil {
		t.Fatalf("ListByUser(A, userA): %v", err)
	}
	if len(rolesA) != 1 || rolesA[0].RoleCode != domain.RoleProducer || rolesA[0].TenantID != tenantA.ID {
		t.Fatalf("ListByUser(A, userA) = %+v, want [producer in tenant A]", rolesA)
	}

	// Tenant B never sees tenant A's membership (RLS), and a user without
	// memberships lists empty.
	rolesAFromB, err := userRoleRepo.ListByUser(ctx, tenantB.ID, userA.ID)
	if err != nil {
		t.Fatalf("ListByUser(B, userA): %v", err)
	}
	if len(rolesAFromB) != 0 {
		t.Fatalf("ListByUser(B, userA) = %+v, want [] (RLS isolation)", rolesAFromB)
	}

	rolesB, err := userRoleRepo.ListByUser(ctx, tenantB.ID, userB.ID)
	if err != nil {
		t.Fatalf("ListByUser(B, userB): %v", err)
	}
	if len(rolesB) != 1 || rolesB[0].RoleCode != domain.RoleAdmin {
		t.Fatalf("ListByUser(B, userB) = %+v, want [admin]", rolesB)
	}
}

// TestUserRoleRepoAssignDuplicateConflict proves assigning the same
// (user_id, role_code) pair twice violates the composite PK (user_id,
// role_code) and surfaces as domain.ErrConflict via mapPgErr (23505).
func TestUserRoleRepoAssignDuplicateConflict(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	userRepo := NewUserRepo(testPool)
	userRoleRepo := NewUserRoleRepo(testPool)

	tenant := createTestTenant(ctx, t, tenantRepo, "Coop A")
	user := createTestUser(ctx, t, userRepo, tenant.ID, "dup@test.local")

	role := &domain.UserRole{UserID: user.ID, RoleCode: domain.RoleAgronomist, TenantID: tenant.ID}
	if err := userRoleRepo.Assign(ctx, role); err != nil {
		t.Fatalf("first Assign: %v", err)
	}
	if err := userRoleRepo.Assign(ctx, role); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second Assign err = %v, want domain.ErrConflict (PK violation)", err)
	}

	roles, err := userRoleRepo.ListByUser(ctx, tenant.ID, user.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("after duplicate Assign ListByUser = %+v, want exactly one membership", roles)
	}
}
