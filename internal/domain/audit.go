package domain

import "time"

// Severity levels for audit entries (slice 3). They drive log level and future
// alerting: critical events (token replay) are logged at ERROR, the rest at
// INFO/WARN.
const (
	SeverityInfo     = "info"
	SeverityWarn     = "warn"
	SeverityCritical = "critical"
)

// AuditEntry records a security-relevant action. It is immutable by design —
// there is no update path, only append. RLS keeps every tenant's audit trail
// isolated so a tenant can never read another's activity.
//
// The chaining fields (slice 3) make the trail tamper-evident: seq is a
// per-tenant contiguous counter starting at 1, prev_hash links to the previous
// entry's chain_hash (64 hex zeros for the genesis entry) and chain_hash is the
// SHA-256 of the canonicalized entry (see services.HashChainEntry).
type AuditEntry struct {
	ID          int64
	TenantID    string
	ActorUserID string
	Action      string
	EntityType  string
	EntityID    string
	Payload     []byte // JSONB payload, kept opaque at the domain level
	CreatedAt   time.Time

	Seq       int64
	PrevHash  string
	ChainHash string
	Severity  string
}

// IsValid checks structural invariants.
func (a AuditEntry) IsValid() bool {
	return a.TenantID != "" && a.Action != "" && a.EntityType != ""
}
