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

// UserRole links a user to a role within one tenant. Roles may carry
// tenant-specific semantics (a user could be producer in tenant A and admin in
// tenant B), hence the composite PK on (user_id, role_code, tenant_id in DB).
type UserRole struct {
	UserID   string
	RoleCode string
	TenantID string
}
