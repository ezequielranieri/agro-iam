package services

import (
	"context"
	"errors"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// fakeLotRepo is an in-memory ports.LotRepository so the service is tested with
// no database or network.
type fakeLotRepo struct {
	created []*domain.Lot
	list    []*domain.Lot
	err     error
}

func (f *fakeLotRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Lot, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeLotRepo) List(ctx context.Context, tenantID string) ([]*domain.Lot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

func (f *fakeLotRepo) Create(ctx context.Context, lot *domain.Lot) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, lot)
	return nil
}

func TestLotServiceCreateHappyPath(t *testing.T) {
	repo := &fakeLotRepo{}
	svc := NewLotService(repo, nil)

	lot, err := svc.Create(context.Background(), "tenant-1", "actor-1", "Campo Norte", 12.5, "soy")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if lot.TenantID != "tenant-1" {
		t.Fatalf("TenantID = %q, want tenant-1", lot.TenantID)
	}
	if lot.ID == "" {
		t.Fatal("a generated lot id must not be empty")
	}
	if lot.Name != "Campo Norte" || lot.AreaHA != 12.5 || lot.Crop != "soy" {
		t.Fatalf("lot fields = %+v, want name=Campo Norte area=12.5 crop=soy", lot)
	}
	if lot.CreatedAt.IsZero() || lot.UpdatedAt.IsZero() {
		t.Fatal("created lot must have timestamps set")
	}
	if !lot.IsValid() {
		t.Fatal("created lot must be valid")
	}
	if len(repo.created) != 1 || repo.created[0] != lot {
		t.Fatal("the repository must have received the created lot")
	}
}

func TestLotServiceCreateRejectsInvalidInput(t *testing.T) {
	repo := &fakeLotRepo{}
	svc := NewLotService(repo, nil)

	cases := []struct {
		label  string
		tenant string
		name   string
		area   float64
	}{
		{"empty name", "tenant-1", "", 1.0},
		{"negative area", "tenant-1", "Campo", -1.0},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			_, err := svc.Create(context.Background(), c.tenant, "actor-1", c.name, c.area, "soy")
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("Create error = %v, want ErrInvalidInput", err)
			}
		})
	}

	if len(repo.created) != 0 {
		t.Fatal("no lot may reach the repository when input is invalid")
	}
}

func TestLotServiceListByTenant(t *testing.T) {
	want := []*domain.Lot{{ID: "lot-1", TenantID: "tenant-1", Name: "Campo Norte"}}
	repo := &fakeLotRepo{list: want}
	svc := NewLotService(repo, nil)

	got, err := svc.ListByTenant(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(got) != 1 || got[0].ID != "lot-1" {
		t.Fatalf("ListByTenant = %+v, want [lot-1]", got)
	}
}
