package services

import (
	"context"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// campaignService implements ports.CampaignService. It owns the business rules
// (input validation via domain.Campaign.IsValid, identity, timestamps) and
// delegates persistence to a ports.CampaignRepository; RLS enforcement is the
// repository's concern, not the service's. Every write emits an audit event
// (info, fail-open) with the authenticated actor.
type campaignService struct {
	campaigns ports.CampaignRepository
	signals   ports.BreachSignalSink
	now       func() time.Time
}

// NewCampaignService wires the repository into a CampaignService. signals may
// be nil (emission is a no-op). The clock is injectable for deterministic
// tests; pass time.Now when unsure.
func NewCampaignService(campaigns ports.CampaignRepository, signals ports.BreachSignalSink) ports.CampaignService {
	return &campaignService{campaigns: campaigns, signals: signals, now: time.Now}
}

// List returns every campaign of the tenant. The tenant id is the scoping key
// that the repository threads into the RLS transaction.
func (s *campaignService) List(ctx context.Context, tenantID string) ([]*domain.Campaign, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}
	return s.campaigns.List(ctx, tenantID)
}

// GetByID returns one campaign or domain.ErrNotFound.
func (s *campaignService) GetByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}
	return s.campaigns.FindByID(ctx, tenantID, id)
}

// Create validates the payload, builds a fresh campaign owned by the tenant
// and persists it. Validation runs via domain.Campaign.IsValid before any SQL
// (name required; started_at must not be after ended_at). The authenticated
// actor is recorded as the audit actor of the event.
func (s *campaignService) Create(ctx context.Context, tenantID, actorUserID string, in ports.CampaignInput) (*domain.Campaign, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}

	now := s.now()
	campaign := &domain.Campaign{
		ID:        newUUIDish(),
		TenantID:  tenantID,
		Name:      in.Name,
		Season:    in.Season,
		StartedAt: in.StartedAt,
		EndedAt:   in.EndedAt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if !campaign.IsValid() {
		return nil, domain.ErrInvalidInput
	}
	if err := s.campaigns.Create(ctx, campaign); err != nil {
		return nil, err
	}

	// Emit campaign.create (info) with the actor. Signal is "" (lot.create
	// parity: the action drives the audit row). Fail-open: a sink error never
	// fails the create.
	emitEvent(ctx, s.signals, tenantID, actorUserID, "", false,
		&Event{Action: "campaign.create", Severity: domain.SeverityInfo, EmitAudit: true}, "")
	return campaign, nil
}

// Update replaces the mutable fields of an existing campaign (full-row replace,
// no partial PATCH semantics). Validation and the ErrNotFound passthrough are
// identical to Create; the audit event is emitted only after a successful
// update, so a missing row is never audited.
func (s *campaignService) Update(ctx context.Context, tenantID, actorUserID, id string, in ports.CampaignInput) (*domain.Campaign, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}

	campaign := &domain.Campaign{
		ID:        id,
		TenantID:  tenantID,
		Name:      in.Name,
		Season:    in.Season,
		StartedAt: in.StartedAt,
		EndedAt:   in.EndedAt,
		UpdatedAt: s.now(),
	}
	if !campaign.IsValid() {
		return nil, domain.ErrInvalidInput
	}
	if err := s.campaigns.Update(ctx, campaign); err != nil {
		return nil, err
	}

	emitEvent(ctx, s.signals, tenantID, actorUserID, "", false,
		&Event{Action: "campaign.update", Severity: domain.SeverityInfo, EmitAudit: true}, "")
	return campaign, nil
}

// Delete removes the campaign. The audit event is emitted only after the
// repository confirms the row existed, so a missing row is never audited.
func (s *campaignService) Delete(ctx context.Context, tenantID, actorUserID, id string) error {
	if tenantID == "" {
		return domain.ErrTenantRequired
	}
	if err := s.campaigns.Delete(ctx, tenantID, id); err != nil {
		return err
	}

	emitEvent(ctx, s.signals, tenantID, actorUserID, "", false,
		&Event{Action: "campaign.delete", Severity: domain.SeverityInfo, EmitAudit: true}, "")
	return nil
}

var _ ports.CampaignService = (*campaignService)(nil)
