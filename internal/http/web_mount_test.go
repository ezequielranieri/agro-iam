package http

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebMountsServeSPA proves the demo frontend is mounted per FR1: GET /
// serves the embedded index.html, GET /static/ serves the asset tree, and
// unknown /api/* paths stay 404 — the API surface is never swallowed by a SPA
// fallback (D1).
func TestWebMountsServeSPA(t *testing.T) {
	tm := newTestTokenManager(t)
	handler := NewServer(nil, tm, nil, &stubCampaignService{}, &stubApplicationService{}, &stubUserService{}, &stubAuditService{}, &stubTenantRepo{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

	// GET / serves the embedded index.html.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET / Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "demo") {
		t.Fatalf("GET / body = %q, want the demo index marker", rec.Body.String())
	}

	// GET /static/app.js serves the embedded asset tree.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.js status = %d, want 200", rec.Code)
	}
}

// TestWebMountsLeaveAPI404 proves the SPA mount does NOT fall back to index
// for unknown API paths: /api/unknown stays 404 (FR1, D1).
func TestWebMountsLeaveAPI404(t *testing.T) {
	tm := newTestTokenManager(t)
	handler := NewServer(nil, tm, nil, &stubCampaignService{}, &stubApplicationService{}, &stubUserService{}, &stubAuditService{}, &stubTenantRepo{}, nil, nil, slog.New(slog.DiscardHandler)).Routes()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/unknown status = %d, want 404 (no SPA fallback for /api/*)", rec.Code)
	}
}
