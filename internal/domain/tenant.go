package domain

import (
	"time"
)

// Tenant is a cooperative or a group of agricultural producers. Tenants own
// every row in every RLS-protected table; this is the root of the tenancy model.
type Tenant struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsValid performs structural validation. It deliberately has no database
// awareness — the repository layer is responsible for uniqueness.
func (t Tenant) IsValid() bool {
	return t.Name != ""
}
