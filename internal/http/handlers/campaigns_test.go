package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
)

// fakeCampaignService is an in-memory ports.CampaignService. The err field
// drives every method so each error branch of the handler is reachable.
type fakeCampaignService struct {
	campaign *domain.Campaign
	list     []*domain.Campaign
	err      error
	input    ports.CampaignInput
	id       string
}

func (f *fakeCampaignService) List(ctx context.Context, tenantID string) ([]*domain.Campaign, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

func (f *fakeCampaignService) GetByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.campaign, nil
}

func (f *fakeCampaignService) Create(ctx context.Context, tenantID, actorUserID string, in ports.CampaignInput) (*domain.Campaign, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.input = in
	return f.campaign, nil
}

func (f *fakeCampaignService) Update(ctx context.Context, tenantID, actorUserID, id string, in ports.CampaignInput) (*domain.Campaign, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.id = id
	f.input = in
	return f.campaign, nil
}

func (f *fakeCampaignService) Delete(ctx context.Context, tenantID, actorUserID, id string) error {
	if f.err != nil {
		return f.err
	}
	f.id = id
	return nil
}

// campaignRequestWithClaims builds a request with authenticated claims for
// tenant-1. Handlers reading a {id} path value set it explicitly (direct
// invocation bypasses the mux).
func campaignRequestWithClaims(method, target string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	return req.WithContext(claims.WithIdentity(req.Context(), "user-1", "tenant-1", "agronomist"))
}

func newCampaignsTestHandler(f *fakeCampaignService) *CampaignsHandler {
	return NewCampaignsHandler(f, slog.New(slog.DiscardHandler))
}

func decodeCampaignResponse(t *testing.T, rec *httptest.ResponseRecorder) campaignResponse {
	t.Helper()
	var resp campaignResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return resp
}

func decodeCampaignError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body["error"]
}

func TestCampaignsList(t *testing.T) {
	want := &domain.Campaign{ID: "c-1", TenantID: "tenant-1", Name: "Campaña 2026", Season: "2026"}
	f := &fakeCampaignService{list: []*domain.Campaign{want}}
	h := newCampaignsTestHandler(f)

	rec := httptest.NewRecorder()
	h.List(rec, campaignRequestWithClaims(http.MethodGet, "/api/v1/campaigns", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Campaigns []campaignResponse `json:"campaigns"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Campaigns) != 1 || resp.Campaigns[0].ID != "c-1" || resp.Campaigns[0].Name != "Campaña 2026" {
		t.Fatalf("campaigns = %+v, want [c-1/Campaña 2026]", resp.Campaigns)
	}
}

func TestCampaignsListUnauthenticated(t *testing.T) {
	h := newCampaignsTestHandler(&fakeCampaignService{})

	// No claims in the context: the tenant is empty, so 401 (never 500).
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCampaignsCreate(t *testing.T) {
	created := &domain.Campaign{ID: "c-new", TenantID: "tenant-1", Name: "Campaña 2026"}
	f := &fakeCampaignService{campaign: created}
	h := newCampaignsTestHandler(f)

	rec := httptest.NewRecorder()
	h.Create(rec, campaignRequestWithClaims(http.MethodPost, "/api/v1/campaigns",
		`{"name":"Campaña 2026","season":"2026"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	resp := decodeCampaignResponse(t, rec)
	if resp.ID != "c-new" || resp.Name != "Campaña 2026" {
		t.Fatalf("response = %+v, want id=c-new name=Campaña 2026", resp)
	}
	if f.input.Name != "Campaña 2026" || f.input.Season != "2026" {
		t.Fatalf("service input = %+v, want name/season forwarded", f.input)
	}
}

func TestCampaignsCreateBadJSON(t *testing.T) {
	h := newCampaignsTestHandler(&fakeCampaignService{})

	rec := httptest.NewRecorder()
	h.Create(rec, campaignRequestWithClaims(http.MethodPost, "/api/v1/campaigns", `{not json`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCampaignsCreateErrorMapping proves the service-error map: invalid input
// 400, tenant required 401, forbidden 403, conflict 409 (update), not found
// 404 (get/update/delete), unknown 500.
func TestCampaignsCreateErrorMapping(t *testing.T) {
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
			h := newCampaignsTestHandler(&fakeCampaignService{err: c.err})

			rec := httptest.NewRecorder()
			h.Create(rec, campaignRequestWithClaims(http.MethodPost, "/api/v1/campaigns",
				`{"name":"Campaña 2026"}`))

			if rec.Code != c.code {
				t.Fatalf("status = %d, want %d", rec.Code, c.code)
			}
			if got := decodeCampaignError(t, rec); got != c.msg {
				t.Fatalf("error message = %q, want %q", got, c.msg)
			}
		})
	}
}

func TestCampaignsGetByID(t *testing.T) {
	f := &fakeCampaignService{campaign: &domain.Campaign{ID: "c-1", TenantID: "tenant-1", Name: "Campaña"}}
	h := newCampaignsTestHandler(f)

	rec := httptest.NewRecorder()
	req := campaignRequestWithClaims(http.MethodGet, "/api/v1/campaigns/c-1", "")
	// Direct handler invocation bypasses the mux, so {id} must be set by hand.
	req.SetPathValue("id", "c-1")
	h.GetByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if resp := decodeCampaignResponse(t, rec); resp.ID != "c-1" {
		t.Fatalf("response id = %q, want c-1", resp.ID)
	}
}

func TestCampaignsGetByIDNotFound(t *testing.T) {
	h := newCampaignsTestHandler(&fakeCampaignService{err: domain.ErrNotFound})

	rec := httptest.NewRecorder()
	req := campaignRequestWithClaims(http.MethodGet, "/api/v1/campaigns/c-missing", "")
	req.SetPathValue("id", "c-missing")
	h.GetByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCampaignsUpdate(t *testing.T) {
	updated := &domain.Campaign{ID: "c-1", TenantID: "tenant-1", Name: "Renombrada"}
	f := &fakeCampaignService{campaign: updated}
	h := newCampaignsTestHandler(f)

	rec := httptest.NewRecorder()
	req := campaignRequestWithClaims(http.MethodPatch, "/api/v1/campaigns/c-1",
		`{"name":"Renombrada"}`)
	req.SetPathValue("id", "c-1")
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if resp := decodeCampaignResponse(t, rec); resp.Name != "Renombrada" {
		t.Fatalf("response name = %q, want Renombrada", resp.Name)
	}
	if f.id != "c-1" {
		t.Fatalf("service received id = %q, want c-1", f.id)
	}
	if f.input.Name != "Renombrada" {
		t.Fatalf("service input name = %q, want Renombrada", f.input.Name)
	}
}

func TestCampaignsDelete(t *testing.T) {
	f := &fakeCampaignService{}
	h := newCampaignsTestHandler(f)

	rec := httptest.NewRecorder()
	req := campaignRequestWithClaims(http.MethodDelete, "/api/v1/campaigns/c-1", "")
	req.SetPathValue("id", "c-1")
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if f.id != "c-1" {
		t.Fatalf("service received id = %q, want c-1", f.id)
	}
}

func TestCampaignsDeleteNotFound(t *testing.T) {
	h := newCampaignsTestHandler(&fakeCampaignService{err: domain.ErrNotFound})

	rec := httptest.NewRecorder()
	req := campaignRequestWithClaims(http.MethodDelete, "/api/v1/campaigns/c-missing", "")
	req.SetPathValue("id", "c-missing")
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
