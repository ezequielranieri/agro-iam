// Package http — breach emission on 429 (PR2-T3 RED phase). A rate-limited
// request must emit the rate-limit-exceeded signal through the Server's sink:
// warn with tenant + request_id when authenticated, Anonymous=true when the
// request has no tenant (the audit sink skips those rows — RLS).
package http

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/application/services"
	"github.com/ezequielranieri/agro-iam/internal/domain"
	"github.com/ezequielranieri/agro-iam/internal/http/claims"
	"github.com/ezequielranieri/agro-iam/internal/requestid"
)

// fakeSignalSink records every SignalEvent the Server emits so 429-emission
// can be asserted.
type fakeSignalSink struct {
	events []ports.SignalEvent
}

func (f *fakeSignalSink) Emit(ctx context.Context, ev ports.SignalEvent) error {
	f.events = append(f.events, ev)
	return nil
}

// TestRateLimit_429EmitsSignalEvent proves an authenticated rate-limited
// request emits security.rate_limit.exceeded/warn with the tenant and the
// correlated request id through the sink.
func TestRateLimit_429EmitsSignalEvent(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	sink := &fakeSignalSink{}
	srv := &Server{
		rateLimiter: limiter,
		signals:     sink,
		log:         slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := srv.rateLimit(1, time.Minute)(stub)

	ctx := claims.WithIdentity(
		requestid.WithRequestID(context.Background(), "req-7"),
		"user-1", "tenant-1", "admin",
	)

	// 1st request allowed.
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).WithContext(ctx)
	req1.RemoteAddr = "192.168.1.1:1111"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("1st request: status = %d, want 200", rec1.Code)
	}

	// 2nd request rate-limited -> emits the warn signal.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/lots", nil).WithContext(ctx)
	req2.RemoteAddr = "192.168.1.1:2222"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("2nd request: status = %d, want 429", rec2.Code)
	}

	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (rate limit exceeded warn)", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Signal != string(services.SignalRateLimitExceeded) ||
		ev.Action != "security.rate_limit.exceeded" || ev.Severity != domain.SeverityWarn {
		t.Fatalf("event = %+v, want rate_limit_exceeded/security.rate_limit.exceeded/warn", ev)
	}
	if ev.TenantID != "tenant-1" || ev.ActorID != "user-1" || ev.RequestID != "req-7" {
		t.Fatalf("event identity = %+v, want tenant-1/user-1/req-7", ev)
	}
}

// TestRateLimit_429AnonymousSetsFlag proves an anonymous rate-limited request
// (auth route, no claims) emits the event with Anonymous=true so the audit
// sink skips the row (slog-only under RLS).
func TestRateLimit_429AnonymousSetsFlag(t *testing.T) {
	limiter := newTestRateLimiter(t)
	defer limiter.Close()

	sink := &fakeSignalSink{}
	srv := &Server{
		rateLimiter: limiter,
		signals:     sink,
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

	if len(sink.events) != 1 {
		t.Fatalf("emitted signals = %d, want 1 (anonymous rate limit exceeded)", len(sink.events))
	}
	ev := sink.events[0]
	if !ev.Anonymous {
		t.Fatalf("event = %+v, want Anonymous=true (slog-only, no audit row under RLS)", ev)
	}
	if ev.TenantID != "" {
		t.Fatalf("event tenant = %q, want empty for an anonymous request", ev.TenantID)
	}
}
