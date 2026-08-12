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

// stubUserService answers every read/write with a harmless result; the route
// tests only prove registration, auth guarding and handler wiring, so the
// responses never matter.
type stubUserService struct{}

func (s *stubUserService) CreateUser(ctx context.Context, tenantID, actorUserID string, in ports.UserInput) (*domain.User, error) {
	return nil, domain.ErrInvalidInput
}
func (s *stubUserService) ListUsers(ctx context.Context, tenantID string) ([]*domain.User, error) {
	return nil, nil
}
func (s *stubUserService) UpdateUser(ctx context.Context, tenantID, actorUserID, id string, in ports.UpdateUserInput) (*domain.User, error) {
	return nil, domain.ErrInvalidInput
}

// stubAuditService answers the audit reads with a harmless result; the route
// tests only prove registration, auth/role guarding and handler wiring, so the
// responses never matter.
type stubAuditService struct{}

func (s *stubAuditService) Record(ctx context.Context, tenantID, actorUserID, action, entityType, entityID string,
	payload []byte, severity string) error {
	return nil
}
func (s *stubAuditService) VerifyChain(ctx context.Context, tenantID string) (int64, error) {
	return 0, nil
}
func (s *stubAuditService) Latest(ctx context.Context, tenantID string, limit int) ([]*domain.AuditEntry, error) {
	return nil, nil
}

// TestCampaignRoutesRegisteredBehindAuth proves the five campaign routes exist
// on the mux and every one is RequireAuth-protected: unauthenticated requests
// get the uniform 401, and an authenticated request reaches the handler (200
// from the stub list), not just the middleware.
func TestCampaignRoutesRegisteredBehindAuth(t *testing.T) {
	tm := newTestTokenManager(t)
	handler := NewServer(nil, tm, nil, &stubCampaignService{}, &stubApplicationService{}, &stubUserService{}, &stubAuditService{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

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
	handler := NewServer(nil, tm, nil, &stubCampaignService{}, &stubApplicationService{}, &stubUserService{}, &stubAuditService{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

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

// TestUserRoutesRegisteredBehindAuth proves the three provisioning routes
// exist on the mux and every one is RequireAuth-protected: unauthenticated
// requests get the uniform 401, and an authenticated request reaches the
// handler (200 from the stub list), not just the middleware. The admin-only
// guard (R12) lands with RequireRole in PR D2.
func TestUserRoutesRegisteredBehindAuth(t *testing.T) {
	tm := newTestTokenManager(t)
	handler := NewServer(nil, tm, nil, &stubCampaignService{}, &stubApplicationService{}, &stubUserService{}, &stubAuditService{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

	// Unauthenticated: each user route collapses to 401, which proves the
	// route is registered AND behind RequireAuth (a missing route would 404).
	paths := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/users"},
		{http.MethodGet, "/api/v1/users"},
		{http.MethodPatch, "/api/v1/users/u-1"},
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
	token := issueAccessToken(t, tm, ports.TokenClaims{UserID: "user-1", TenantID: "tenant-1", Role: "admin"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/users with token: status = %d, want 200", rec.Code)
	}
}

// TestRouteRoleMatrix proves the R15 route×role authorization matrix end to
// end: write routes accept exactly their allowed role set, reads accept any
// authenticated user, and a denied role collapses to the uniform 403 before
// the handler runs.
func TestRouteRoleMatrix(t *testing.T) {
	tm := newTestTokenManager(t)
	handler := NewServer(nil, tm, nil, &stubCampaignService{}, &stubApplicationService{}, &stubUserService{}, &stubAuditService{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

	roles := []string{domain.RoleAdmin, domain.RoleAgronomist, domain.RoleProducer, domain.RoleAuditor, domain.RoleHauler, ""}

	routes := []struct {
		name   string
		method string
		path   string
		allow  []string // nil = any authenticated role (including roleless)
	}{
		// Reads: any authenticated user.
		{"campaign list", http.MethodGet, "/api/v1/campaigns", nil},
		{"campaign get", http.MethodGet, "/api/v1/campaigns/c-1", nil},
		{"application list", http.MethodGet, "/api/v1/applications", nil},
		{"application get", http.MethodGet, "/api/v1/applications/a-1", nil},
		// Campaign writes: admin | agronomist.
		{"campaign create", http.MethodPost, "/api/v1/campaigns", []string{domain.RoleAdmin, domain.RoleAgronomist}},
		{"campaign update", http.MethodPatch, "/api/v1/campaigns/c-1", []string{domain.RoleAdmin, domain.RoleAgronomist}},
		{"campaign delete", http.MethodDelete, "/api/v1/campaigns/c-1", []string{domain.RoleAdmin, domain.RoleAgronomist}},
		// Application writes: admin | agronomist | producer.
		{"application create", http.MethodPost, "/api/v1/applications", []string{domain.RoleAdmin, domain.RoleAgronomist, domain.RoleProducer}},
		{"application update", http.MethodPatch, "/api/v1/applications/a-1", []string{domain.RoleAdmin, domain.RoleAgronomist, domain.RoleProducer}},
		{"application delete", http.MethodDelete, "/api/v1/applications/a-1", []string{domain.RoleAdmin, domain.RoleAgronomist, domain.RoleProducer}},
		// Provisioning: admin only (reads included, R12).
		{"user create", http.MethodPost, "/api/v1/users", []string{domain.RoleAdmin}},
		{"user list", http.MethodGet, "/api/v1/users", []string{domain.RoleAdmin}},
		{"user update", http.MethodPatch, "/api/v1/users/u-1", []string{domain.RoleAdmin}},
		// Audit trail: admin only (AP1).
		{"audit list", http.MethodGet, "/api/v1/audit", []string{domain.RoleAdmin}},
	}

	for _, route := range routes {
		for _, role := range roles {
			t.Run(route.name+"/"+role, func(t *testing.T) {
				wantAllowed := route.allow == nil
				for _, allowed := range route.allow {
					if allowed == role {
						wantAllowed = true
						break
					}
				}

				token := issueAccessToken(t, tm, ports.TokenClaims{UserID: "user-1", TenantID: "tenant-1", Role: role})
				req := httptest.NewRequest(route.method, route.path, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if wantAllowed {
					if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
						t.Fatalf("role %q allowed on %s %s but got %d", role, route.method, route.path, rec.Code)
					}
					return
				}
				if rec.Code != http.StatusForbidden {
					t.Fatalf("role %q denied on %s %s: got %d, want 403", role, route.method, route.path, rec.Code)
				}
				if got := rec.Body.String(); got != "{\"error\":\"forbidden\"}\n" {
					t.Fatalf("denied body = %q, want uniform forbidden JSON", got)
				}
			})
		}
	}
}

// TestAuthRoutesStayPublic proves the auth endpoints are unchanged: they carry
// no auth or role guard, so an unauthenticated request never sees 401/403.
func TestAuthRoutesStayPublic(t *testing.T) {
	handler := NewServer(nil, newTestTokenManager(t), nil, &stubCampaignService{}, &stubApplicationService{}, &stubUserService{}, &stubAuditService{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

	for _, p := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/refresh"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(p.method, p.path, nil))
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Fatalf("%s %s: status = %d, auth routes must stay public (no auth/role guard)", p.method, p.path, rec.Code)
		}
	}
}
