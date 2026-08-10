package services

import (
	"context"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// applicationService implements ports.ApplicationService. It owns the business
// rules (input validation via domain.Application.IsValid, identity, applied_at
// defaulting) and delegates persistence to a ports.ApplicationRepository; RLS
// enforcement is the repository's concern, not the service's. Every write
// emits an audit event (info, fail-open) with the authenticated actor.
type applicationService struct {
	applications ports.ApplicationRepository
	signals      ports.BreachSignalSink
	now          func() time.Time
}

// NewApplicationService wires the repository into an ApplicationService.
// signals may be nil (emission is a no-op). The clock is injectable for
// deterministic tests; pass time.Now when unsure.
func NewApplicationService(applications ports.ApplicationRepository, signals ports.BreachSignalSink) ports.ApplicationService {
	return &applicationService{applications: applications, signals: signals, now: time.Now}
}

// List returns every application of the tenant. The tenant id is the scoping
// key that the repository threads into the RLS transaction.
func (s *applicationService) List(ctx context.Context, tenantID string) ([]*domain.Application, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}
	return s.applications.List(ctx, tenantID)
}

// GetByID returns one application or domain.ErrNotFound.
func (s *applicationService) GetByID(ctx context.Context, tenantID, id string) (*domain.Application, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}
	return s.applications.FindByID(ctx, tenantID, id)
}

// Create validates the payload, builds a fresh application owned by the tenant
// and persists it. Validation runs via domain.Application.IsValid before any
// SQL (lot_id, campaign_id and product_name required). A zero AppliedAt is
// defaulted to the service clock (schema column NOT NULL DEFAULT now()). The
// authenticated actor is recorded as the audit actor of the event.
func (s *applicationService) Create(ctx context.Context, tenantID, actorUserID string, in ports.ApplicationInput) (*domain.Application, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}

	now := s.now()
	app := &domain.Application{
		ID:          newUUIDish(),
		TenantID:    tenantID,
		LotID:       in.LotID,
		CampaignID:  in.CampaignID,
		ProductName: in.ProductName,
		Dose:        in.Dose,
		AppliedAt:   in.AppliedAt,
		OperatorID:  in.OperatorID,
		Notes:       in.Notes,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if app.AppliedAt.IsZero() {
		app.AppliedAt = now
	}
	if !app.IsValid() {
		return nil, domain.ErrInvalidInput
	}
	if err := s.applications.Create(ctx, app); err != nil {
		return nil, err
	}

	// Emit application.create (info) with the actor. Signal is "" (lot.create
	// parity: the action drives the audit row). Fail-open: a sink error never
	// fails the create.
	emitEvent(ctx, s.signals, tenantID, actorUserID, "", false,
		&Event{Action: "application.create", Severity: domain.SeverityInfo, EmitAudit: true}, "")
	return app, nil
}

// Update replaces the mutable fields of an existing application (full-row
// replace, no partial PATCH semantics). Validation and the ErrNotFound
// passthrough are identical to Create; the audit event is emitted only after a
// successful update, so a missing row is never audited.
func (s *applicationService) Update(ctx context.Context, tenantID, actorUserID, id string, in ports.ApplicationInput) (*domain.Application, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}

	app := &domain.Application{
		ID:          id,
		TenantID:    tenantID,
		LotID:       in.LotID,
		CampaignID:  in.CampaignID,
		ProductName: in.ProductName,
		Dose:        in.Dose,
		AppliedAt:   in.AppliedAt,
		OperatorID:  in.OperatorID,
		Notes:       in.Notes,
		UpdatedAt:   s.now(),
	}
	if app.AppliedAt.IsZero() {
		app.AppliedAt = app.UpdatedAt
	}
	if !app.IsValid() {
		return nil, domain.ErrInvalidInput
	}
	if err := s.applications.Update(ctx, app); err != nil {
		return nil, err
	}

	emitEvent(ctx, s.signals, tenantID, actorUserID, "", false,
		&Event{Action: "application.update", Severity: domain.SeverityInfo, EmitAudit: true}, "")
	return app, nil
}

// Delete removes the application. The audit event is emitted only after the
// repository confirms the row existed, so a missing row is never audited.
func (s *applicationService) Delete(ctx context.Context, tenantID, actorUserID, id string) error {
	if tenantID == "" {
		return domain.ErrTenantRequired
	}
	if err := s.applications.Delete(ctx, tenantID, id); err != nil {
		return err
	}

	emitEvent(ctx, s.signals, tenantID, actorUserID, "", false,
		&Event{Action: "application.delete", Severity: domain.SeverityInfo, EmitAudit: true}, "")
	return nil
}

var _ ports.ApplicationService = (*applicationService)(nil)
