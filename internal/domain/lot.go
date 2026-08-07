package domain

import "time"

// Lot is a physical parcel of land (lote). It is owned by a tenant, so the PK
// is composite (id, tenant_id) in the database — this defends against a crafted
// lot_id from another tenant leaking through a join or FK.
type Lot struct {
	ID       string
	TenantID string
	Name     string
	AreaHA   float64
	Crop     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsValid checks structural invariants. area_ha must be positive when set.
func (l Lot) IsValid() bool {
	return l.TenantID != "" && l.Name != "" && l.AreaHA >= 0
}
