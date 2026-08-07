package domain

import "time"

// Application is a single input application (aplicación de insumos): which
// product was applied, at what dose, on which lot and campaign, by whom.
type Application struct {
	ID          string
	TenantID    string
	LotID       string
	CampaignID  string
	ProductName string
	Dose        string
	AppliedAt   time.Time
	OperatorID  string
	Notes       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IsValid checks structural invariants. Product name and both FKs are required.
func (a Application) IsValid() bool {
	return a.TenantID != "" &&
		a.LotID != "" &&
		a.CampaignID != "" &&
		a.ProductName != ""
}
