package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// fakeCampaignRepo is an in-memory ports.CampaignRepository so the service is
// tested with no database or network.
type fakeCampaignRepo struct {
	campaign *domain.Campaign
	list     []*domain.Campaign
	err      error
	created  []*domain.Campaign
	updated  []*domain.Campaign
	deleted  []string
}

func (f *fakeCampaignRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.campaign == nil {
		return nil, domain.ErrNotFound
	}
	return f.campaign, nil
}

func (f *fakeCampaignRepo) List(ctx context.Context, tenantID string) ([]*domain.Campaign, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

func (f *fakeCampaignRepo) Create(ctx context.Context, campaign *domain.Campaign) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, campaign)
	return nil
}

func (f *fakeCampaignRepo) Update(ctx context.Context, campaign *domain.Campaign) error {
	if f.err != nil {
		return f.err
	}
	f.updated = append(f.updated, campaign)
	return nil
}

func (f *fakeCampaignRepo) Delete(ctx context.Context, tenantID, id string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

var (
	testCampaignStart = time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	testCampaignEnd   = time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
)

// TestCampaignServiceCreateHappyPath proves Create builds a fresh campaign
// (uuid + tenant + timestamps), persists it and emits campaign.create (info)
// with the actor through the sink.
func TestCampaignServiceCreateHappyPath(t *testing.T) {
	repo := &fakeCampaignRepo{}
	sink := &recordingSink{}
	svc := NewCampaignService(repo, sink)

	campaign, err := svc.Create(context.Background(), "tenant-1", "actor-1", ports.CampaignInput{
		Name:      "Campaña 2026",
		Season:    "2026",
		StartedAt: &testCampaignStart,
		EndedAt:   &testCampaignEnd,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if campaign.TenantID != "tenant-1" {
		t.Fatalf("TenantID = %q, want tenant-1", campaign.TenantID)
	}
	if campaign.ID == "" {
		t.Fatal("a generated campaign id must not be empty")
	}
	if campaign.Name != "Campaña 2026" || campaign.Season != "2026" {
		t.Fatalf("campaign fields = %+v, want name=Campaña 2026 season=2026", campaign)
	}
	if campaign.StartedAt == nil || !campaign.StartedAt.Equal(testCampaignStart) ||
		campaign.EndedAt == nil || !campaign.EndedAt.Equal(testCampaignEnd) {
		t.Fatalf("campaign dates = %+v, want start=%v end=%v", campaign.StartedAt, testCampaignStart, testCampaignEnd)
	}
	if campaign.CreatedAt.IsZero() || campaign.UpdatedAt.IsZero() {
		t.Fatal("created campaign must have timestamps set")
	}
	if !campaign.IsValid() {
		t.Fatal("created campaign must be valid")
	}
	if len(repo.created) != 1 || repo.created[0] != campaign {
		t.Fatal("the repository must have received the created campaign")
	}

	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (campaign.create)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Signal != "" || ev.Action != "campaign.create" || ev.Severity != domain.SeverityInfo {
		t.Fatalf("event = %+v, want Signal=\"\" campaign.create/info", ev)
	}
	if ev.TenantID != "tenant-1" || ev.ActorID != "actor-1" {
		t.Fatalf("event identity = %+v, want tenant-1/actor-1", ev)
	}
}

// TestCampaignServiceCreateRejectsInvalidInput proves IsValid failures
// (empty name, ended_at before started_at) surface as ErrInvalidInput and no
// row reaches the repository.
func TestCampaignServiceCreateRejectsInvalidInput(t *testing.T) {
	repo := &fakeCampaignRepo{}
	svc := NewCampaignService(repo, nil)

	start := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		label string
		in    ports.CampaignInput
	}{
		{"empty name", ports.CampaignInput{Name: "", StartedAt: &start, EndedAt: &end}},
		{"inverted dates", ports.CampaignInput{Name: "Campaña", StartedAt: &end, EndedAt: &start}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			_, err := svc.Create(context.Background(), "tenant-1", "actor-1", c.in)
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("Create error = %v, want ErrInvalidInput", err)
			}
		})
	}

	if len(repo.created) != 0 {
		t.Fatal("no campaign may reach the repository when input is invalid")
	}
}

// TestCampaignServiceRequiresTenant proves every operation rejects an empty
// tenant with ErrTenantRequired before any validation or persistence.
func TestCampaignServiceRequiresTenant(t *testing.T) {
	repo := &fakeCampaignRepo{}
	sink := &recordingSink{}
	svc := NewCampaignService(repo, sink)

	in := ports.CampaignInput{Name: "Campaña", StartedAt: &testCampaignStart, EndedAt: &testCampaignEnd}
	ctx := context.Background()

	if _, err := svc.Create(ctx, "", "actor-1", in); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("Create with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if _, err := svc.Update(ctx, "", "actor-1", "c-1", in); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("Update with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if err := svc.Delete(ctx, "", "actor-1", "c-1"); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("Delete with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if _, err := svc.List(ctx, ""); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("List with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if _, err := svc.GetByID(ctx, "", "c-1"); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("GetByID with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if len(sink.events) != 0 {
		t.Fatal("no audit event may be emitted when the tenant is missing")
	}
	if len(repo.created)+len(repo.updated)+len(repo.deleted) != 0 {
		t.Fatal("no repository call may happen when the tenant is missing")
	}
}

// TestCampaignServiceUpdateEmitsAudit proves Update replaces the mutable
// fields and emits campaign.update (info) with the actor.
func TestCampaignServiceUpdateEmitsAudit(t *testing.T) {
	repo := &fakeCampaignRepo{}
	sink := &recordingSink{}
	svc := NewCampaignService(repo, sink)

	campaign, err := svc.Update(context.Background(), "tenant-1", "actor-1", "c-1", ports.CampaignInput{
		Name:      "Campaña revisada",
		Season:    "2026",
		StartedAt: &testCampaignStart,
		EndedAt:   &testCampaignEnd,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if campaign.ID != "c-1" || campaign.TenantID != "tenant-1" || campaign.Name != "Campaña revisada" {
		t.Fatalf("updated campaign = %+v, want id=c-1 tenant=tenant-1 name=Campaña revisada", campaign)
	}
	if len(repo.updated) != 1 || repo.updated[0] != campaign {
		t.Fatal("the repository must have received the updated campaign")
	}
	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (campaign.update)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Action != "campaign.update" || ev.Severity != domain.SeverityInfo || ev.ActorID != "actor-1" {
		t.Fatalf("event = %+v, want campaign.update/info/actor-1", ev)
	}
}

// TestCampaignServiceUpdateNotFoundNoAudit proves a missing row (repo returns
// ErrNotFound) propagates and no audit event is emitted.
func TestCampaignServiceUpdateNotFoundNoAudit(t *testing.T) {
	repo := &fakeCampaignRepo{err: domain.ErrNotFound}
	sink := &recordingSink{}
	svc := NewCampaignService(repo, sink)

	_, err := svc.Update(context.Background(), "tenant-1", "actor-1", "c-missing", ports.CampaignInput{
		Name: "Campaña", StartedAt: &testCampaignStart, EndedAt: &testCampaignEnd,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update error = %v, want ErrNotFound", err)
	}
	if len(sink.events) != 0 {
		t.Fatal("no audit event may be emitted when the update found no row")
	}
}

// TestCampaignServiceDeleteEmitsAudit proves Delete removes the campaign and
// emits campaign.delete (info) with the actor.
func TestCampaignServiceDeleteEmitsAudit(t *testing.T) {
	repo := &fakeCampaignRepo{}
	sink := &recordingSink{}
	svc := NewCampaignService(repo, sink)

	if err := svc.Delete(context.Background(), "tenant-1", "actor-1", "c-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "c-1" {
		t.Fatalf("deleted ids = %v, want [c-1]", repo.deleted)
	}
	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (campaign.delete)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Action != "campaign.delete" || ev.Severity != domain.SeverityInfo || ev.ActorID != "actor-1" {
		t.Fatalf("event = %+v, want campaign.delete/info/actor-1", ev)
	}
}

// TestCampaignServiceDeleteNotFoundNoAudit proves a missing row propagates as
// ErrNotFound and no audit event is emitted.
func TestCampaignServiceDeleteNotFoundNoAudit(t *testing.T) {
	repo := &fakeCampaignRepo{err: domain.ErrNotFound}
	sink := &recordingSink{}
	svc := NewCampaignService(repo, sink)

	if err := svc.Delete(context.Background(), "tenant-1", "actor-1", "c-missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete error = %v, want ErrNotFound", err)
	}
	if len(sink.events) != 0 {
		t.Fatal("no audit event may be emitted when the delete found no row")
	}
}

// TestCampaignServiceListAndGetByID prove the read paths pass through to the
// repository with the tenant scoping key.
func TestCampaignServiceListAndGetByID(t *testing.T) {
	want := &domain.Campaign{ID: "c-1", TenantID: "tenant-1", Name: "Campaña 2026"}
	repo := &fakeCampaignRepo{campaign: want, list: []*domain.Campaign{want}}
	svc := NewCampaignService(repo, nil)

	got, err := svc.List(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "c-1" {
		t.Fatalf("List = %+v, want [c-1]", got)
	}

	one, err := svc.GetByID(context.Background(), "tenant-1", "c-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if one != want {
		t.Fatalf("GetByID = %+v, want %+v", one, want)
	}
}
