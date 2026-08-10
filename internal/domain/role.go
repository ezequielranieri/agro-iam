package domain

// Role codes are the fixed vocabulary of the agricultural workflow. The roles
// table is a global catalog shared by every tenant (no tenant_id column),
// exactly like tenants itself — neither is a target for RLS.
//
//	granted to    | typical member
//	--------------+-------------------------------
//	admin         | cooperative manager
//	producer      | field owner / productor
//	agronomist    | crop advisor / agrónomo
//	auditor       | compliance reviewer
//	hauler        | transportista
type Role struct {
	Code        string
	Name        string
	Description string
}

// Role codes (kept as constants so application logic never typo's them).
const (
	RoleAdmin      = "admin"
	RoleProducer   = "producer"
	RoleAgronomist = "agronomist"
	RoleAuditor    = "auditor"
	RoleHauler     = "hauler"
)

// UserRole links a user to a role within one tenant. A user belongs to exactly
// one tenant (users.tenant_id), so the composite PK on app.user_roles is
// (user_id, role_code) — tenant_id is a plain column, NOT part of the PK. The
// tenant_id column exists so RLS can scope every row; the PK uniqueness is the
// one-role-per-user-per-tenant constraint.
type UserRole struct {
	UserID   string
	RoleCode string
	TenantID string
}

// IsValidRoleCode reports whether code is part of the fixed role vocabulary
// (R10). Role codes are validated in the application layer BEFORE any write so
// an unknown code never reaches SQL (the roles table is a global catalog and
// there is no FK-style enforcement in the service path).
func IsValidRoleCode(code string) bool {
	switch code {
	case RoleAdmin, RoleProducer, RoleAgronomist, RoleAuditor, RoleHauler:
		return true
	}
	return false
}
