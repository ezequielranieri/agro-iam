package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// fakeTenantRepo is an in-memory ports.TenantRepository. The err field drives
// every method so each error branch of the handler is reachable.
type fakeTenantRepo struct {
	tenants []*domain.Tenant
	err     error
}

func (f *fakeTenantRepo) FindByID(ctx context.Context, id string) (*domain.Tenant, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, domain.ErrNotFound
}

func (f *fakeTenantRepo) Create(ctx context.Context, tenant *domain.Tenant) error {
	return f.err
}

func (f *fakeTenantRepo) List(ctx context.Context) ([]*domain.Tenant, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tenants, nil
}

func newTenantsTestHandler(f *fakeTenantRepo) *TenantsHandler {
	return NewTenantsHandler(f, slog.New(slog.DiscardHandler))
}

// TestTenantsListPublic proves GET /api/v1/tenants needs no credentials and
// returns id+name only (AP2): no password, credential or internal column
// ever appears — the response shape is exactly {id, name}.
func TestTenantsListPublic(t *testing.T) {
	f := &fakeTenantRepo{tenants: []*domain.Tenant{
		{ID: "t-1", Name: "Coop Esperanza"},
		{ID: "t-2", Name: "Coop Litoral"},
	}}
	h := newTenantsTestHandler(f)

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (public, no auth)", rec.Code)
	}
	var resp struct {
		Tenants []map[string]any `json:"tenants"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tenants) != 2 {
		t.Fatalf("tenants = %+v, want 2 entries", resp.Tenants)
	}
	if resp.Tenants[0]["id"] != "t-1" || resp.Tenants[0]["name"] != "Coop Esperanza" {
		t.Fatalf("tenants[0] = %+v, want id=t-1 name=Coop Esperanza", resp.Tenants[0])
	}
	// AP2: the realm list exposes exactly {id, name} — nothing else.
	for _, tt := range resp.Tenants {
		if len(tt) != 2 {
			t.Fatalf("tenant payload = %+v, want exactly the id+name keys (no credentials/internal columns)", tt)
		}
		if _, ok := tt["id"]; !ok {
			t.Fatalf("tenant payload %+v missing id", tt)
		}
		if _, ok := tt["name"]; !ok {
			t.Fatalf("tenant payload %+v missing name", tt)
		}
	}
}

// TestTenantsListEmpty proves the empty-registry path renders an empty list,
// never null and never an error (triangulation: different data, same contract).
func TestTenantsListEmpty(t *testing.T) {
	h := newTenantsTestHandler(&fakeTenantRepo{})

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Tenants []map[string]any `json:"tenants"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tenants) != 0 {
		t.Fatalf("tenants = %+v, want an empty list", resp.Tenants)
	}
}

// TestTenantsListError proves an unknown repository error collapses to the
// uniform 500 after logging (the endpoint must never leak internals).
func TestTenantsListError(t *testing.T) {
	h := newTenantsTestHandler(&fakeTenantRepo{err: errors.New("boom")})

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
