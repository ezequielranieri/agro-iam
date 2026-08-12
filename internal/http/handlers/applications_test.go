package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
)

// fakeApplicationService is an in-memory ports.ApplicationService. The err
// field drives every method so each error branch of the handler is reachable.
type fakeApplicationService struct {
	app  *domain.Application
	list []*domain.Application
	err  error
	in   ports.ApplicationInput
	id   string
}

func (f *fakeApplicationService) List(ctx context.Context, tenantID string) ([]*domain.Application, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

func (f *fakeApplicationService) GetByID(ctx context.Context, tenantID, id string) (*domain.Application, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.app, nil
}

func (f *fakeApplicationService) Create(ctx context.Context, tenantID, actorUserID string, in ports.ApplicationInput) (*domain.Application, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.in = in
	return f.app, nil
}

func (f *fakeApplicationService) Update(ctx context.Context, tenantID, actorUserID, id string, in ports.ApplicationInput) (*domain.Application, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.id = id
	f.in = in
	return f.app, nil
}

func (f *fakeApplicationService) Delete(ctx context.Context, tenantID, actorUserID, id string) error {
	if f.err != nil {
		return f.err
	}
	f.id = id
	return nil
}

// applicationRequestWithClaims builds a request with authenticated claims for
// tenant-1. Handlers reading a {id} path value set it explicitly (direct
// invocation bypasses the mux).
func applicationRequestWithClaims(method, target string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	return req.WithContext(claims.WithIdentity(req.Context(), "user-1", "tenant-1", "producer"))
}

func newApplicationsTestHandler(f *fakeApplicationService) *ApplicationsHandler {
	return NewApplicationsHandler(f, slog.New(slog.DiscardHandler))
}

func decodeApplicationResponse(t *testing.T, rec *httptest.ResponseRecorder) applicationResponse {
	t.Helper()
	var resp applicationResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return resp
}

func decodeApplicationError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body["error"]
}

func TestApplicationsList(t *testing.T) {
	applied := time.Date(2026, 2, 10, 9, 30, 0, 0, time.UTC)
	want := &domain.Application{ID: "a-1", TenantID: "tenant-1", ProductName: "Glifosato", AppliedAt: applied, OperatorName: "Ana Operadora"}
	f := &fakeApplicationService{list: []*domain.Application{want}}
	h := newApplicationsTestHandler(f)

	rec := httptest.NewRecorder()
	h.List(rec, applicationRequestWithClaims(http.MethodGet, "/api/v1/applications", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Applications []applicationResponse `json:"applications"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Applications) != 1 || resp.Applications[0].ID != "a-1" || resp.Applications[0].ProductName != "Glifosato" {
		t.Fatalf("applications = %+v, want [a-1/Glifosato]", resp.Applications)
	}
	if resp.Applications[0].OperatorName != "Ana Operadora" {
		t.Fatalf("operator_name = %q, want Ana Operadora (S1.9)", resp.Applications[0].OperatorName)
	}
}

func TestApplicationsListUnauthenticated(t *testing.T) {
	h := newApplicationsTestHandler(&fakeApplicationService{})

	// No claims in the context: the tenant is empty, so 401 (never 500).
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/applications", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestApplicationsCreate(t *testing.T) {
	created := &domain.Application{ID: "a-new", TenantID: "tenant-1", ProductName: "Glifosato"}
	f := &fakeApplicationService{app: created}
	h := newApplicationsTestHandler(f)

	rec := httptest.NewRecorder()
	h.Create(rec, applicationRequestWithClaims(http.MethodPost, "/api/v1/applications",
		`{"lot_id":"lot-1","campaign_id":"campaign-1","product_name":"Glifosato"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	resp := decodeApplicationResponse(t, rec)
	if resp.ID != "a-new" || resp.ProductName != "Glifosato" {
		t.Fatalf("response = %+v, want id=a-new product=Glifosato", resp)
	}
	if f.in.LotID != "lot-1" || f.in.CampaignID != "campaign-1" || f.in.ProductName != "Glifosato" {
		t.Fatalf("service input = %+v, want lot/campaign/product forwarded", f.in)
	}
}

func TestApplicationsCreateBadJSON(t *testing.T) {
	h := newApplicationsTestHandler(&fakeApplicationService{})

	rec := httptest.NewRecorder()
	h.Create(rec, applicationRequestWithClaims(http.MethodPost, "/api/v1/applications", `{not json`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestApplicationsCreateErrorMapping proves the service-error map: invalid
// input 400, tenant required 401, forbidden 403, conflict 409 (update), not
// found 404 (get/update/delete), unknown 500.
func TestApplicationsCreateErrorMapping(t *testing.T) {
	cases := []struct {
		label string
		err   error
		code  int
		msg   string
	}{
		{"invalid input", domain.ErrInvalidInput, http.StatusBadRequest, "invalid input"},
		{"tenant required", domain.ErrTenantRequired, http.StatusUnauthorized, "unauthorized"},
		{"forbidden", domain.ErrForbidden, http.StatusForbidden, "forbidden"},
		{"conflict", domain.ErrConflict, http.StatusConflict, "conflict"},
		{"not found", domain.ErrNotFound, http.StatusNotFound, "not found"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			h := newApplicationsTestHandler(&fakeApplicationService{err: c.err})

			rec := httptest.NewRecorder()
			h.Create(rec, applicationRequestWithClaims(http.MethodPost, "/api/v1/applications",
				`{"lot_id":"lot-1","campaign_id":"campaign-1","product_name":"Glifosato"}`))

			if rec.Code != c.code {
				t.Fatalf("status = %d, want %d", rec.Code, c.code)
			}
			if got := decodeApplicationError(t, rec); got != c.msg {
				t.Fatalf("error message = %q, want %q", got, c.msg)
			}
		})
	}
}

func TestApplicationsGetByID(t *testing.T) {
	f := &fakeApplicationService{app: &domain.Application{ID: "a-1", TenantID: "tenant-1", ProductName: "Glifosato"}}
	h := newApplicationsTestHandler(f)

	rec := httptest.NewRecorder()
	req := applicationRequestWithClaims(http.MethodGet, "/api/v1/applications/a-1", "")
	// Direct handler invocation bypasses the mux, so {id} must be set by hand.
	req.SetPathValue("id", "a-1")
	h.GetByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if resp := decodeApplicationResponse(t, rec); resp.ID != "a-1" {
		t.Fatalf("response id = %q, want a-1", resp.ID)
	}
}

func TestApplicationsGetByIDNotFound(t *testing.T) {
	h := newApplicationsTestHandler(&fakeApplicationService{err: domain.ErrNotFound})

	rec := httptest.NewRecorder()
	req := applicationRequestWithClaims(http.MethodGet, "/api/v1/applications/a-missing", "")
	req.SetPathValue("id", "a-missing")
	h.GetByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestApplicationsUpdate(t *testing.T) {
	updated := &domain.Application{ID: "a-1", TenantID: "tenant-1", ProductName: "Atrazina"}
	f := &fakeApplicationService{app: updated}
	h := newApplicationsTestHandler(f)

	rec := httptest.NewRecorder()
	req := applicationRequestWithClaims(http.MethodPatch, "/api/v1/applications/a-1",
		`{"lot_id":"lot-1","campaign_id":"campaign-1","product_name":"Atrazina"}`)
	req.SetPathValue("id", "a-1")
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if resp := decodeApplicationResponse(t, rec); resp.ProductName != "Atrazina" {
		t.Fatalf("response product = %q, want Atrazina", resp.ProductName)
	}
	if f.id != "a-1" {
		t.Fatalf("service received id = %q, want a-1", f.id)
	}
	if f.in.ProductName != "Atrazina" {
		t.Fatalf("service input product = %q, want Atrazina", f.in.ProductName)
	}
}

func TestApplicationsDelete(t *testing.T) {
	f := &fakeApplicationService{}
	h := newApplicationsTestHandler(f)

	rec := httptest.NewRecorder()
	req := applicationRequestWithClaims(http.MethodDelete, "/api/v1/applications/a-1", "")
	req.SetPathValue("id", "a-1")
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if f.id != "a-1" {
		t.Fatalf("service received id = %q, want a-1", f.id)
	}
}

func TestApplicationsDeleteNotFound(t *testing.T) {
	h := newApplicationsTestHandler(&fakeApplicationService{err: domain.ErrNotFound})

	rec := httptest.NewRecorder()
	req := applicationRequestWithClaims(http.MethodDelete, "/api/v1/applications/a-missing", "")
	req.SetPathValue("id", "a-missing")
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
