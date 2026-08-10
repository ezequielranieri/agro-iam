package http

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// stubCampaignService answers every read/write with a harmless result; the
// route tests only prove registration, auth guarding and handler wiring, so
// the responses never matter.
type stubCampaignService struct{}

func (s *stubCampaignService) List(ctx context.Context, tenantID string) ([]*domain.Campaign, error) {
	return nil, nil
}
func (s *stubCampaignService) GetByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	return nil, domain.ErrNotFound
}
func (s *stubCampaignService) Create(ctx context.Context, tenantID, actorUserID string, in ports.CampaignInput) (*domain.Campaign, error) {
	return nil, domain.ErrInvalidInput
}
func (s *stubCampaignService) Update(ctx context.Context, tenantID, actorUserID, id string, in ports.CampaignInput) (*domain.Campaign, error) {
	return nil, domain.ErrInvalidInput
}
func (s *stubCampaignService) Delete(ctx context.Context, tenantID, actorUserID, id string) error {
	return domain.ErrNotFound
}

// stubApplicationService answers every read/write with a harmless result; the
// route tests only prove registration, auth guarding and handler wiring, so
// the responses never matter.
type stubApplicationService struct{}

func (s *stubApplicationService) List(ctx context.Context, tenantID string) ([]*domain.Application, error) {
	return nil, nil
}
func (s *stubApplicationService) GetByID(ctx context.Context, tenantID, id string) (*domain.Application, error) {
	return nil, domain.ErrNotFound
}
func (s *stubApplicationService) Create(ctx context.Context, tenantID, actorUserID string, in ports.ApplicationInput) (*domain.Application, error) {
	return nil, domain.ErrInvalidInput
}
func (s *stubApplicationService) Update(ctx context.Context, tenantID, actorUserID, id string, in ports.ApplicationInput) (*domain.Application, error) {
	return nil, domain.ErrInvalidInput
}
func (s *stubApplicationService) Delete(ctx context.Context, tenantID, actorUserID, id string) error {
	return domain.ErrNotFound
}

// TestCampaignRoutesRegisteredBehindAuth proves the five campaign routes exist
// on the mux and every one is RequireAuth-protected: unauthenticated requests
// get the uniform 401, and an authenticated request reaches the handler (200
// from the stub list), not just the middleware.
func TestCampaignRoutesRegisteredBehindAuth(t *testing.T) {
	tm := newTestTokenManager(t)
	handler := NewServer(nil, tm, nil, &stubCampaignService{}, &stubApplicationService{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

	// Unauthenticated: each campaign route collapses to 401, which proves the
	// route is registered AND behind RequireAuth (a missing route would 404).
	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/campaigns"},
		{http.MethodPost, "/api/v1/campaigns"},
		{http.MethodGet, "/api/v1/campaigns/c-1"},
		{http.MethodPatch, "/api/v1/campaigns/c-1"},
		{http.MethodDelete, "/api/v1/campaigns/c-1"},
	}
	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token: status = %d, want 401", p.method, p.path, rec.Code)
		}
	}

	// Authenticated: a valid token passes RequireAuth and reaches the handler.
	token := issueAccessToken(t, tm, ports.TokenClaims{UserID: "user-1", TenantID: "tenant-1", Role: "agronomist"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/campaigns with token: status = %d, want 200", rec.Code)
	}

	// Health probe stays public: the mux is live, not everything 401s.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status = %d, want 200", rec.Code)
	}
}

// TestApplicationRoutesRegisteredBehindAuth proves the five application routes
// exist on the mux and every one is RequireAuth-protected: unauthenticated
// requests get the uniform 401, and an authenticated request reaches the
// handler (200 from the stub list), not just the middleware.
func TestApplicationRoutesRegisteredBehindAuth(t *testing.T) {
	tm := newTestTokenManager(t)
	handler := NewServer(nil, tm, nil, &stubCampaignService{}, &stubApplicationService{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

	// Unauthenticated: each application route collapses to 401, which proves
	// the route is registered AND behind RequireAuth (a missing route would 404).
	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/applications"},
		{http.MethodPost, "/api/v1/applications"},
		{http.MethodGet, "/api/v1/applications/a-1"},
		{http.MethodPatch, "/api/v1/applications/a-1"},
		{http.MethodDelete, "/api/v1/applications/a-1"},
	}
	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token: status = %d, want 401", p.method, p.path, rec.Code)
		}
	}

	// Authenticated: a valid token passes RequireAuth and reaches the handler.
	token := issueAccessToken(t, tm, ports.TokenClaims{UserID: "user-1", TenantID: "tenant-1", Role: "producer"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/applications with token: status = %d, want 200", rec.Code)
	}
}
