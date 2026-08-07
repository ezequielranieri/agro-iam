package domain

import "time"

// AuditEntry records a security-relevant action. It is immutable by design —
// there is no update path, only append. RLS keeps every tenant's audit trail
// isolated so a tenant can never read another's activity.
type AuditEntry struct {
	ID         int64
	TenantID   string
	ActorUserID string
	Action     string
	EntityType string
	EntityID   string
	Payload    []byte // JSONB payload, kept opaque at the domain level
	CreatedAt  time.Time
}

// IsValid checks structural invariants.
func (a AuditEntry) IsValid() bool {
	return a.TenantID != "" && a.Action != "" && a.EntityType != ""
}
