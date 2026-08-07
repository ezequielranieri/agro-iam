package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
	"github.com/ezequielranieri/agro-iam/internal/infrastructure/auth"
)

// RequireAuth is pure HTTP logic: it needs a real TokenManager (built with a
// test secret) but no database, so the whole middleware is unit-tested here.
// Importing infrastructure/auth in a test is the exception the ports rule
// allows â€” the middleware contract is verified against the real verifier.

func newTestTokenManager(t *testing.T) ports.TokenManager {
	t.Helper()
	tm, err := auth.NewTokenManager("slice-1-test-secret-not-too-short", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	return tm
}

func issueAccessToken(t *testing.T, tm ports.TokenManager, claims ports.TokenClaims) string {
	t.Helper()
	token, err := tm.Issue(claims)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return token
}

func TestRequireAuthInjectsClaimsIntoContext(t *testing.T) {
	tm := newTestTokenManager(t)
	token := issueAccessToken(t, tm, ports.TokenClaims{UserID: "user-1", TenantID: "tenant-1", Role: "admin"})

	var gotUser, gotTenant, gotRole string
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = claims.UserIDFrom(r.Context())
		gotTenant = claims.TenantIDFrom(r.Context())
		gotRole = claims.RoleFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	RequireAuth(tm)(stub).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotUser != "user-1" || gotTenant != "tenant-1" || gotRole != "admin" {
		t.Fatalf("context claims = (%q, %q, %q), want (user-1, tenant-1, admin)", gotUser, gotTenant, gotRole)
	}
}

func TestRequireAuthRejectsMissingOrInvalidCredentials(t *testing.T) {
	tm := newTestTokenManager(t)
	// The stub answers 200; every rejected request must stay 401, which also
	// proves the stub was never reached.
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		label       string
		authzHeader string // empty means "no header at all"
	}{
		{"missing header", ""},
		{"wrong scheme", "Basic dXNlcjpwYXNz"},
		{"empty bearer token", "Bearer "},
		{"garbage token", "Bearer not.a.jwt"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil)
			if c.authzHeader != "" {
				req.Header.Set("Authorization", c.authzHeader)
			}
			rec := httptest.NewRecorder()

			RequireAuth(tm)(stub).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Body.String(); got != "{\"error\":\"unauthorized\"}\n" {
				t.Fatalf("body = %q, want unauthorized JSON", got)
			}
		})
	}
}
