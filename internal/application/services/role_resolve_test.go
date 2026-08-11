package services

import (
	"context"
	"errors"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// TestResolveRolePrecedence pins the D3 precedence table: when a user holds
// several memberships, the single most-privileged code wins
// (admin > agronomist > producer > auditor > hauler). A roleless user — no
// memberships, or an empty slice — resolves to the empty string, which is
// legal: an empty claim is not an error.
func TestResolveRolePrecedence(t *testing.T) {
	member := func(code string) *domain.UserRole {
		return &domain.UserRole{UserID: "u-1", TenantID: "tenant-1", RoleCode: code}
	}
	cases := []struct {
		name  string
		roles []*domain.UserRole
		want  string
	}{
		{"admin beats agronomist", []*domain.UserRole{member(domain.RoleAgronomist), member(domain.RoleAdmin)}, domain.RoleAdmin},
		{"agronomist beats producer", []*domain.UserRole{member(domain.RoleProducer), member(domain.RoleAgronomist)}, domain.RoleAgronomist},
		{"producer beats auditor", []*domain.UserRole{member(domain.RoleAuditor), member(domain.RoleProducer)}, domain.RoleProducer},
		{"auditor beats hauler", []*domain.UserRole{member(domain.RoleHauler), member(domain.RoleAuditor)}, domain.RoleAuditor},
		{"single hauler", []*domain.UserRole{member(domain.RoleHauler)}, domain.RoleHauler},
		{"single admin", []*domain.UserRole{member(domain.RoleAdmin)}, domain.RoleAdmin},
		{"roleless user resolves empty", nil, ""},
		{"empty membership slice resolves empty", []*domain.UserRole{}, ""},
		{"unknown codes ignored", []*domain.UserRole{member("superuser")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roles := &fakeUserRoleRepo{roles: tc.roles}
			got, err := resolveRole(context.Background(), roles, "tenant-1", "u-1")
			if err != nil {
				t.Fatalf("resolveRole: %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveRole = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveRoleErrorPropagates proves fail-closed: a repository error must
// propagate to the caller, never degrade into a silently roleless resolution.
func TestResolveRoleErrorPropagates(t *testing.T) {
	roles := &fakeUserRoleRepo{listErr: errors.New("db down")}
	_, err := resolveRole(context.Background(), roles, "tenant-1", "u-1")
	if err == nil {
		t.Fatal("resolveRole error must propagate (fail-closed), got nil")
	}
}
