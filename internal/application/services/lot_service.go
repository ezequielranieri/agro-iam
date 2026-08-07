package services

import (
	"context"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// lotService implements ports.LotService. It owns the business rules (input
// validation, identity, timestamps) and delegates persistence to a
// ports.LotRepository; RLS enforcement is the repository's concern, not the
// service's.
type lotService struct {
	lots ports.LotRepository
	now  func() time.Time
}

// NewLotService wires the repository into a LotService. The clock is injectable
// for deterministic tests; pass time.Now when unsure.
func NewLotService(lots ports.LotRepository) ports.LotService {
	return &lotService{lots: lots, now: time.Now}
}

// ListByTenant returns every lot of the tenant. The tenant id is the scoping
// key that the repository threads into the RLS transaction.
func (s *lotService) ListByTenant(ctx context.Context, tenantID string) ([]*domain.Lot, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}
	return s.lots.List(ctx, tenantID)
}

// Create validates the payload, builds a fresh lot owned by the tenant and
// persists it. Empty name and negative area are rejected before any SQL runs.
func (s *lotService) Create(ctx context.Context, tenantID, name string, areaHA float64, crop string) (*domain.Lot, error) {
	if tenantID == "" || name == "" {
		return nil, domain.ErrInvalidInput
	}
	if areaHA < 0 {
		return nil, domain.ErrInvalidInput
	}

	now := s.now()
	lot := &domain.Lot{
		ID:        newUUIDish(),
		TenantID:  tenantID,
		Name:      name,
		AreaHA:    areaHA,
		Crop:      crop,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.lots.Create(ctx, lot); err != nil {
		return nil, err
	}
	return lot, nil
}

var _ ports.LotService = (*lotService)(nil)
