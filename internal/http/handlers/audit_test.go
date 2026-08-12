package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
)

// fakeAuditService is an in-memory ports.AuditService. The err field drives
// Latest so every error branch of the handler is reachable; the received
// tenant/limit are recorded so the handler's forwarding is assertable.
type fakeAuditService struct {
	entries   []*domain.AuditEntry
	err       error
	gotTenant string
	gotLimit  int
}

func (f *fakeAuditService) Record(ctx context.Context, tenantID, actorUserID, action, entityType, entityID string,
	payload []byte, severity string) error {
	return nil
}

func (f *fakeAuditService) VerifyChain(ctx context.Context, tenantID string) (int64, error) { return 0, nil }

func (f *fakeAuditService) Latest(ctx context.Context, tenantID string, limit int) ([]*domain.AuditEntry, error) {
	f.gotTenant = tenantID
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func newAuditTestHandler(f *fakeAuditService) *AuditHandler {
	return NewAuditHandler(f, slog.New(slog.DiscardHandler))
}

// auditRequestWithClaims builds an authenticated admin request for tenant-1.
func auditRequestWithClaims(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	return req.WithContext(claims.WithIdentity(req.Context(), "user-1", "tenant-1", "admin"))
}

// TestAuditLatest proves GET /api/v1/audit returns the tenant's latest-N
// entries (newest first) with the event/severity columns the demo table
// renders, forwarding the authenticated tenant and the latest-100 window (AP1).
func TestAuditLatest(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	f := &fakeAuditService{entries: []*domain.AuditEntry{
		{Seq: 2, TenantID: "tenant-1", ActorUserID: "u-1", Action: "auth.refresh", EntityType: "refresh_token", EntityID: "f-2", Severity: domain.SeverityInfo, CreatedAt: now},
		{Seq: 1, TenantID: "tenant-1", ActorUserID: "u-1", Action: "auth.login", EntityType: "user", EntityID: "u-1", Severity: domain.SeverityWarn, CreatedAt: now.Add(-time.Hour)},
	}}
	h := newAuditTestHandler(f)

	rec := httptest.NewRecorder()
	h.Latest(rec, auditRequestWithClaims(http.MethodGet, "/api/v1/audit"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if f.gotTenant != "tenant-1" {
		t.Fatalf("service tenant = %q, want tenant-1 (from claims, never the client)", f.gotTenant)
	}
	if f.gotLimit != 100 {
		t.Fatalf("service limit = %d, want 100 (latest-100)", f.gotLimit)
	}

	var resp struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %+v, want 2 entries", resp.Events)
	}
	first := resp.Events[0]
	if first["seq"] != float64(2) || first["action"] != "auth.refresh" || first["severity"] != "info" {
		t.Fatalf("event[0] = %+v, want seq=2 action=auth.refresh severity=info", first)
	}
	if first["actor_user_id"] != "u-1" || first["entity_type"] != "refresh_token" || first["entity_id"] != "f-2" {
		t.Fatalf("event[0] = %+v, want actor/entity columns", first)
	}
	if _, ok := first["created_at"]; !ok {
		t.Fatal("event[0] missing created_at")
	}
	if resp.Events[1]["severity"] != "warn" {
		t.Fatalf("event[1] severity = %v, want warn (severity column travels)", resp.Events[1]["severity"])
	}
}

// TestAuditLatestUnauthenticated proves the handler 401s when no claims were
// injected (a request that bypassed RequireAuth never 500s).
func TestAuditLatestUnauthenticated(t *testing.T) {
	h := newAuditTestHandler(&fakeAuditService{})

	rec := httptest.NewRecorder()
	h.Latest(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestAuditLatestErrorMapping proves the service-error map: not found 404,
// tenant required 401, unknown 500.
func TestAuditLatestErrorMapping(t *testing.T) {
	cases := []struct {
		label string
		err   error
		code  int
		msg   string
	}{
		{"not found", domain.ErrNotFound, http.StatusNotFound, "not found"},
		{"tenant required", domain.ErrTenantRequired, http.StatusUnauthorized, "unauthorized"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			h := newAuditTestHandler(&fakeAuditService{err: c.err})

			rec := httptest.NewRecorder()
			h.Latest(rec, auditRequestWithClaims(http.MethodGet, "/api/v1/audit"))

			if rec.Code != c.code {
				t.Fatalf("status = %d, want %d", rec.Code, c.code)
			}
			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body["error"] != c.msg {
				t.Fatalf("error message = %q, want %q", body["error"], c.msg)
			}
		})
	}
}

// TestAuditLatestInternalError proves an unknown service error collapses to the
// uniform 500 after logging.
func TestAuditLatestInternalError(t *testing.T) {
	h := newAuditTestHandler(&fakeAuditService{err: errors.New("boom")})

	rec := httptest.NewRecorder()
	h.Latest(rec, auditRequestWithClaims(http.MethodGet, "/api/v1/audit"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
