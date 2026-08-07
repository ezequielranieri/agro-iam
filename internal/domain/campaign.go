package domain

import "time"

// Campaign is an agricultural season (campaña): the bounded window in which
// applications are applied and evaluated. Owned by a tenant.
type Campaign struct {
	ID        string
	TenantID  string
	Name      string
	Season    string
	StartedAt *time.Time
	EndedAt   *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsValid checks structural invariants. If both dates are present the start
// must not be after the end.
func (c Campaign) IsValid() bool {
	if c.TenantID == "" || c.Name == "" {
		return false
	}
	if c.StartedAt != nil && c.EndedAt != nil && c.EndedAt.Before(*c.StartedAt) {
		return false
	}
	return true
}
