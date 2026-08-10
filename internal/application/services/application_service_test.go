package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// fakeApplicationRepo is an in-memory ports.ApplicationRepository so the
// service is tested with no database or network.
type fakeApplicationRepo struct {
	app     *domain.Application
	list    []*domain.Application
	err     error
	created []*domain.Application
	updated []*domain.Application
	deleted []string
}

func (f *fakeApplicationRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Application, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.app == nil {
		return nil, domain.ErrNotFound
	}
	return f.app, nil
}

func (f *fakeApplicationRepo) List(ctx context.Context, tenantID string) ([]*domain.Application, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

func (f *fakeApplicationRepo) Create(ctx context.Context, app *domain.Application) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, app)
	return nil
}

func (f *fakeApplicationRepo) Update(ctx context.Context, app *domain.Application) error {
	if f.err != nil {
		return f.err
	}
	f.updated = append(f.updated, app)
	return nil
}

func (f *fakeApplicationRepo) Delete(ctx context.Context, tenantID, id string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

var testAppliedAt = time.Date(2026, 2, 10, 9, 30, 0, 0, time.UTC)

func validApplicationInput() ports.ApplicationInput {
	return ports.ApplicationInput{
		LotID:       "lot-1",
		CampaignID:  "campaign-1",
		ProductName: "Glifosato",
		Dose:        "3 l/ha",
		AppliedAt:   testAppliedAt,
		OperatorID:  "operator-1",
		Notes:       "campo norte",
	}
}

// TestApplicationServiceCreateHappyPath proves Create builds a fresh
// application (uuid + tenant + timestamps), forwards every input field
// (operator id included — empty→NULL mapping is the repository's job),
// persists it and emits application.create (info) with the actor.
func TestApplicationServiceCreateHappyPath(t *testing.T) {
	repo := &fakeApplicationRepo{}
	sink := &recordingSink{}
	svc := NewApplicationService(repo, sink)

	app, err := svc.Create(context.Background(), "tenant-1", "actor-1", validApplicationInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if app.TenantID != "tenant-1" {
		t.Fatalf("TenantID = %q, want tenant-1", app.TenantID)
	}
	if app.ID == "" {
		t.Fatal("a generated application id must not be empty")
	}
	if app.LotID != "lot-1" || app.CampaignID != "campaign-1" || app.ProductName != "Glifosato" ||
		app.Dose != "3 l/ha" || app.Notes != "campo norte" || app.OperatorID != "operator-1" {
		t.Fatalf("application fields = %+v, want all input fields forwarded", app)
	}
	if !app.AppliedAt.Equal(testAppliedAt) {
		t.Fatalf("AppliedAt = %v, want %v", app.AppliedAt, testAppliedAt)
	}
	if app.CreatedAt.IsZero() || app.UpdatedAt.IsZero() {
		t.Fatal("created application must have timestamps set")
	}
	if !app.IsValid() {
		t.Fatal("created application must be valid")
	}
	if len(repo.created) != 1 || repo.created[0] != app {
		t.Fatal("the repository must have received the created application")
	}

	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (application.create)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Signal != "" || ev.Action != "application.create" || ev.Severity != domain.SeverityInfo {
		t.Fatalf("event = %+v, want Signal=\"\" application.create/info", ev)
	}
	if ev.TenantID != "tenant-1" || ev.ActorID != "actor-1" {
		t.Fatalf("event identity = %+v, want tenant-1/actor-1", ev)
	}
}

// TestApplicationServiceCreateDefaultsAppliedAt proves a zero AppliedAt is
// defaulted to the service clock instead of persisting the zero time.
func TestApplicationServiceCreateDefaultsAppliedAt(t *testing.T) {
	repo := &fakeApplicationRepo{}
	frozen := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	svc := NewApplicationService(repo, nil)
	svc.(*applicationService).now = func() time.Time { return frozen }

	in := validApplicationInput()
	in.AppliedAt = time.Time{}
	if _, err := svc.Create(context.Background(), "tenant-1", "actor-1", in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := repo.created[0].AppliedAt; !got.Equal(frozen) {
		t.Fatalf("AppliedAt = %v, want defaulted to service clock %v", got, frozen)
	}
}

// TestApplicationServiceCreateRejectsInvalidInput proves IsValid failures
// (empty lot_id, empty campaign_id, empty product_name) surface as
// ErrInvalidInput and no row reaches the repository.
func TestApplicationServiceCreateRejectsInvalidInput(t *testing.T) {
	repo := &fakeApplicationRepo{}
	svc := NewApplicationService(repo, nil)

	cases := []struct {
		label string
		blank func(*ports.ApplicationInput)
	}{
		{"empty lot_id", func(in *ports.ApplicationInput) { in.LotID = "" }},
		{"empty campaign_id", func(in *ports.ApplicationInput) { in.CampaignID = "" }},
		{"empty product_name", func(in *ports.ApplicationInput) { in.ProductName = "" }},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			in := validApplicationInput()
			c.blank(&in)
			_, err := svc.Create(context.Background(), "tenant-1", "actor-1", in)
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("Create error = %v, want ErrInvalidInput", err)
			}
		})
	}

	if len(repo.created) != 0 {
		t.Fatal("no application may reach the repository when input is invalid")
	}
}

// TestApplicationServiceRequiresTenant proves every operation rejects an empty
// tenant with ErrTenantRequired before validation or persistence.
func TestApplicationServiceRequiresTenant(t *testing.T) {
	repo := &fakeApplicationRepo{}
	sink := &recordingSink{}
	svc := NewApplicationService(repo, sink)

	in := validApplicationInput()
	ctx := context.Background()

	if _, err := svc.Create(ctx, "", "actor-1", in); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("Create with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if _, err := svc.Update(ctx, "", "actor-1", "a-1", in); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("Update with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if err := svc.Delete(ctx, "", "actor-1", "a-1"); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("Delete with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if _, err := svc.List(ctx, ""); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("List with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if _, err := svc.GetByID(ctx, "", "a-1"); !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("GetByID with empty tenant error = %v, want ErrTenantRequired", err)
	}
	if len(sink.events) != 0 {
		t.Fatal("no audit event may be emitted when the tenant is missing")
	}
	if len(repo.created)+len(repo.updated)+len(repo.deleted) != 0 {
		t.Fatal("no repository call may happen when the tenant is missing")
	}
}

// TestApplicationServiceUpdateEmitsAudit proves Update replaces the mutable
// fields and emits application.update (info) with the actor.
func TestApplicationServiceUpdateEmitsAudit(t *testing.T) {
	repo := &fakeApplicationRepo{}
	sink := &recordingSink{}
	svc := NewApplicationService(repo, sink)

	in := validApplicationInput()
	in.ProductName = "Atrazina"
	app, err := svc.Update(context.Background(), "tenant-1", "actor-1", "a-1", in)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if app.ID != "a-1" || app.TenantID != "tenant-1" || app.ProductName != "Atrazina" {
		t.Fatalf("updated application = %+v, want id=a-1 tenant=tenant-1 product=Atrazina", app)
	}
	if len(repo.updated) != 1 || repo.updated[0] != app {
		t.Fatal("the repository must have received the updated application")
	}
	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (application.update)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Action != "application.update" || ev.Severity != domain.SeverityInfo || ev.ActorID != "actor-1" {
		t.Fatalf("event = %+v, want application.update/info/actor-1", ev)
	}
}

// TestApplicationServiceWritesNotFoundNoAudit proves a missing row (repo
// returns ErrNotFound) propagates from Update and Delete and no audit event is
// emitted for either.
func TestApplicationServiceWritesNotFoundNoAudit(t *testing.T) {
	cases := []struct {
		label string
		run   func(svc ports.ApplicationService) error
	}{
		{"update", func(svc ports.ApplicationService) error {
			_, err := svc.Update(context.Background(), "tenant-1", "actor-1", "a-missing", validApplicationInput())
			return err
		}},
		{"delete", func(svc ports.ApplicationService) error {
			return svc.Delete(context.Background(), "tenant-1", "actor-1", "a-missing")
		}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			sink := &recordingSink{}
			svc := NewApplicationService(&fakeApplicationRepo{err: domain.ErrNotFound}, sink)

			if err := c.run(svc); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("%s error = %v, want ErrNotFound", c.label, err)
			}
			if len(sink.events) != 0 {
				t.Fatalf("%s with no row emitted %d audit events, want 0", c.label, len(sink.events))
			}
		})
	}
}

// TestApplicationServiceDeleteEmitsAudit proves Delete removes the application
// and emits application.delete (info) with the actor.
func TestApplicationServiceDeleteEmitsAudit(t *testing.T) {
	repo := &fakeApplicationRepo{}
	sink := &recordingSink{}
	svc := NewApplicationService(repo, sink)

	if err := svc.Delete(context.Background(), "tenant-1", "actor-1", "a-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "a-1" {
		t.Fatalf("deleted ids = %v, want [a-1]", repo.deleted)
	}
	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (application.delete)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Action != "application.delete" || ev.Severity != domain.SeverityInfo || ev.ActorID != "actor-1" {
		t.Fatalf("event = %+v, want application.delete/info/actor-1", ev)
	}
}

// TestApplicationServiceListAndGetByID prove the read paths pass through to the
// repository with the tenant scoping key.
func TestApplicationServiceListAndGetByID(t *testing.T) {
	want := &domain.Application{ID: "a-1", TenantID: "tenant-1", ProductName: "Glifosato"}
	repo := &fakeApplicationRepo{app: want, list: []*domain.Application{want}}
	svc := NewApplicationService(repo, nil)

	got, err := svc.List(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a-1" {
		t.Fatalf("List = %+v, want [a-1]", got)
	}

	one, err := svc.GetByID(context.Background(), "tenant-1", "a-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if one != want {
		t.Fatalf("GetByID = %+v, want %+v", one, want)
	}
}
