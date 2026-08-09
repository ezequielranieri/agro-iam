// Package http — breach emission on 429 (PR3-T5 RED phase). A rate-limited
// request must emit the rate-limit-exceeded event: an audit warn row when the
// request is authenticated (claims present), slog-only when anonymous.
package http

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
)

// fakeAuditRecorder records audit Record calls for 429-emission assertions.
type fakeAuditRecorder struct {
	calls []string // "action:severity" per Record
}

func (f *fakeAuditRecorder) Record(ctx context.Context, tenantID, actorUserID, action, entityType, entityID string,
	payload []byte, severity string) error {
	f.calls = append(f.calls, action+":"+severity)
	return nil
}

func (f *fakeAuditRecorder) VerifyChain(ctx context.Context, tenantID string) (int64, error) {
	return 0, nil
}

// TestRateLimit_429EmitsAuditWarn proves an authenticated rate-limited request
// emits security.rate_limit.exceeded/warn through the audit service.
func TestRateLimit_429EmitsAuditWarn(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	audit := &fakeAuditRecorder{}
	srv := &Server{
		rateLimiter: limiter,
		audit:       audit,
		log:         slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := srv.rateLimit(1, time.Minute)(stub)

	ctx := claims.WithIdentity(context.Background(), "user-1", "tenant-1", "admin")

	// 1st request allowed.
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).WithContext(ctx)
	req1.RemoteAddr = "192.168.1.1:1111"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("1st request: status = %d, want 200", rec1.Code)
	}

	// 2nd request rate-limited -> emits the warn event.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).WithContext(ctx)
	req2.RemoteAddr = "192.168.1.1:2222"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("2nd request: status = %d, want 429", rec2.Code)
	}

	if len(audit.calls) != 1 {
		t.Fatalf("audit calls = %d, want 1 (rate limit exceeded warn)", len(audit.calls))
	}
	if audit.calls[0] != "security.rate_limit.exceeded:"+domain.SeverityWarn {
		t.Fatalf("audit call = %q, want security.rate_limit.exceeded:warn", audit.calls[0])
	}
}

// TestRateLimit_429AnonymousSlogOnly proves an anonymous rate-limited request
// (auth route, no claims) never writes an audit row — slog only.
func TestRateLimit_429AnonymousSlogOnly(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	audit := &fakeAuditRecorder{}
	srv := &Server{
		rateLimiter: limiter,
		audit:       audit,
		log:         slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := srv.rateLimit(1, time.Minute)(stub)

	// Anonymous requests to /api/v1/auth/login (no claims).
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if len(audit.calls) != 0 {
		t.Fatalf("anonymous 429 must not write audit rows, got %d", len(audit.calls))
	}
}
