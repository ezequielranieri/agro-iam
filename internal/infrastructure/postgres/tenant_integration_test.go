package postgres

import (
	"testing"
)

// TestTenantRepoList proves TenantRepository.List returns every tenant of the
// global (RLS-free) registry with its id+name (AP2). Tenants are deliberately
// NOT RLS-protected — unlike every tenanted table, a raw query without tenant
// context must still see them, because the public realm list reads at request
// time and must survive reseeds.
func TestTenantRepoList(t *testing.T) {
	ctx := setupIntegrationTest(t)
	repo := NewTenantRepo(testPool)

	createTestTenant(ctx, t, repo, "Coop Esperanza")
	createTestTenant(ctx, t, repo, "Coop Litoral")

	tenants, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tenants) != 2 {
		t.Fatalf("List = %d tenants, want 2", len(tenants))
	}
	names := map[string]bool{}
	for _, tt := range tenants {
		if tt.ID == "" || tt.Name == "" {
			t.Fatalf("tenant = %+v, want a non-empty id and name", tt)
		}
		names[tt.Name] = true
	}
	if !names["Coop Esperanza"] || !names["Coop Litoral"] {
		t.Fatalf("List names = %v, want both seeded tenants", names)
	}

	// The registry is global: no tenant context is required to read it, so the
	// demo login screen can fetch the realm list before any authentication.
	var rawCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM app.tenants`).Scan(&rawCount); err != nil {
		t.Fatalf("count tenants without tenant context: %v", err)
	}
	if rawCount != 2 {
		t.Fatalf("raw SELECT without tenant context saw %d tenants, want 2 (global registry)", rawCount)
	}
}
