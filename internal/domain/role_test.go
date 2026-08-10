package domain

import "testing"

// TestIsValidRoleCode accepts the five fixed role codes of the agricultural
// workflow vocabulary (R10): admin, agronomist, producer, auditor, hauler.
// Anything else — including the empty string and look-alikes — is rejected so
// an unknown role code can never reach SQL.
func TestIsValidRoleCode(t *testing.T) {
	accepted := []string{RoleAdmin, RoleAgronomist, RoleProducer, RoleAuditor, RoleHauler}
	for _, code := range accepted {
		if !IsValidRoleCode(code) {
			t.Fatalf("IsValidRoleCode(%q) = false, want true", code)
		}
	}

	rejected := []string{"", "superuser", "admin ", "ADMIN", "manager"}
	for _, code := range rejected {
		if IsValidRoleCode(code) {
			t.Fatalf("IsValidRoleCode(%q) = true, want false", code)
		}
	}
}
