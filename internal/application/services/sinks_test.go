// Package services — sink adapter tests (M30 breach-signal substrate, PR1).
// Written RED-first: they reference ports.SignalEvent, SlogSink, AuditSink and
// NewFanOut before those symbols exist. They pin the R4/R5 contracts: slog
// parity, audit skip rules (RLS), nil/empty no-op and fail-open fan-out.
package services

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// captureLogger returns a TextHandler-backed logger whose output the test can
// assert on. Local helper so this file stays self-contained.
func captureLogger(t *testing.T) (*slog.Logger, *strings.Builder) {
	t.Helper()
	var buf strings.Builder
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// fakeAudit records Record calls. It is a local stand-in for ports.AuditService.
type fakeAudit struct {
	calls []auditRecord
}

type auditRecord struct {
	tenantID, actorID, action, entityType, entityID string
	payload                                         []byte
	severity                                        string
}

func (f *fakeAudit) Record(ctx context.Context, tenantID, actorUserID, action, entityType, entityID string,
	payload []byte, severity string) error {
	f.calls = append(f.calls, auditRecord{
		tenantID: tenantID, actorID: actorUserID, action: action,
		entityType: entityType, entityID: entityID, payload: payload, severity: severity,
	})
	return nil
}

func (f *fakeAudit) VerifyChain(ctx context.Context, tenantID string) (int64, error) { return 0, nil }

// recordingSink records every SignalEvent it receives; it can be configured to
// fail or panic so the fan-out behavior is observable.
type recordingSink struct {
	events []ports.SignalEvent
	err    error
	panic  bool
}

func (s *recordingSink) Emit(ctx context.Context, ev ports.SignalEvent) error {
	if s.panic {
		panic("boom")
	}
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, ev)
	return nil
}

// orderSink appends to a shared slice so invocation order can be asserted.
type orderSink struct{ rec *[]ports.SignalEvent }

func (s *orderSink) Emit(ctx context.Context, ev ports.SignalEvent) error {
	*s.rec = append(*s.rec, ev)
	return nil
}

// TestSlogSinkSeverityToLevel proves the severity→level mapping and the exact
// slice-3 attr set: msg "security", action/severity/tenant/actor/request_id.
func TestSlogSinkSeverityToLevel(t *testing.T) {
	cases := []struct {
		name     string
		severity string
		level    string
	}{
		{"critical maps to Error", domain.SeverityCritical, "level=ERROR"},
		{"warn maps to Warn", domain.SeverityWarn, "level=WARN"},
		{"info maps to Info", domain.SeverityInfo, "level=INFO"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log, buf := captureLogger(t)
			sink := NewSlogSink(log)
			ev := ports.SignalEvent{
				Signal: "login_success", Action: "auth.login", Severity: tc.severity,
				TenantID: "tenant-1", ActorID: "user-1", RequestID: "req-7",
			}
			if err := sink.Emit(context.Background(), ev); err != nil {
				t.Fatalf("Emit: %v", err)
			}
			out := buf.String()
			for _, want := range []string{
				tc.level, "msg=security", "action=auth.login",
				"severity=" + tc.severity, "tenant=tenant-1", "actor=user-1", "request_id=req-7",
			} {
				if !strings.Contains(out, want) {
					t.Fatalf("log output missing %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestSlogSinkAnonymousStillLogged proves anonymous events are slog-only
// (slog-always, DECISIONS 2.15) — the sink never drops them.
func TestSlogSinkAnonymousStillLogged(t *testing.T) {
	log, buf := captureLogger(t)
	sink := NewSlogSink(log)
	ev := ports.SignalEvent{
		Action: "security.rate_limit.exceeded", Severity: domain.SeverityWarn, Anonymous: true,
	}
	if err := sink.Emit(context.Background(), ev); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "msg=security") {
		t.Fatalf("anonymous event must still be slogged (slog-always), got:\n%s", out)
	}
}

// TestAuditSinkSkipsAnonymous proves RLS: an anonymous event never becomes an
// audit row, even when a tenant string is present.
func TestAuditSinkSkipsAnonymous(t *testing.T) {
	audit := &fakeAudit{}
	sink := NewAuditSink(audit, discardLogger())
	ev := ports.SignalEvent{
		Action: "security.rate_limit.exceeded", Severity: domain.SeverityWarn,
		TenantID: "tenant-1", Anonymous: true,
	}
	if err := sink.Emit(context.Background(), ev); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(audit.calls) != 0 {
		t.Fatalf("audit calls = %d, want 0 (anonymous is slog-only under RLS)", len(audit.calls))
	}
}

// TestAuditSinkSkipsTenantless proves a tenantless non-anonymous event is also
// skipped: no tenant owns the row.
func TestAuditSinkSkipsTenantless(t *testing.T) {
	audit := &fakeAudit{}
	sink := NewAuditSink(audit, discardLogger())
	ev := ports.SignalEvent{Action: "auth.login", Severity: domain.SeverityInfo, TenantID: ""}
	if err := sink.Emit(context.Background(), ev); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(audit.calls) != 0 {
		t.Fatalf("audit calls = %d, want 0 (tenantless event has no owning tenant)", len(audit.calls))
	}
}

// TestAuditSinkPassesRecordArgs proves the Record call carries the exact args
// the audit row needs: tenant, actor, action (as entity type AND entity id),
// nil payload and the severity.
func TestAuditSinkPassesRecordArgs(t *testing.T) {
	audit := &fakeAudit{}
	sink := NewAuditSink(audit, discardLogger())
	ev := ports.SignalEvent{
		Signal: "refresh_replay", Action: "auth.refresh.replay", Severity: domain.SeverityCritical,
		TenantID: "tenant-1", ActorID: "user-1", RequestID: "req-9",
	}
	if err := sink.Emit(context.Background(), ev); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(audit.calls) != 1 {
		t.Fatalf("audit calls = %d, want 1", len(audit.calls))
	}
	got := audit.calls[0]
	if got.tenantID != "tenant-1" || got.actorID != "user-1" ||
		got.action != "auth.refresh.replay" || got.entityType != "security" ||
		got.entityID != "auth.refresh.replay" || got.severity != domain.SeverityCritical {
		t.Fatalf("record = %+v, want tenant-1/user-1/auth.refresh.replay/security/critical", got)
	}
	if len(got.payload) != 0 {
		t.Fatalf("payload = %v, want nil", got.payload)
	}
}

// TestAuditSinkNilAuditNoOp proves fail-open: a nil audit service must not
// panic nor fail the emit.
func TestAuditSinkNilAuditNoOp(t *testing.T) {
	sink := NewAuditSink(nil, discardLogger())
	ev := ports.SignalEvent{Action: "auth.login", Severity: domain.SeverityInfo, TenantID: "tenant-1"}
	if err := sink.Emit(context.Background(), ev); err != nil {
		t.Fatalf("Emit: %v (fail-open must tolerate a nil audit service)", err)
	}
}

// TestFanOutEmptyAndNilSinksNoOp proves R4: an empty or all-nil sink list is a
// no-op and the emit completes without error.
func TestFanOutEmptyAndNilSinksNoOp(t *testing.T) {
	cases := []struct {
		name string
		fo   ports.BreachSignalSink
	}{
		{"empty sink list", NewFanOut(discardLogger())},
		{"all-nil sinks", NewFanOut(discardLogger(), nil, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fo.Emit(context.Background(), ports.SignalEvent{Action: "auth.login"}); err != nil {
				t.Fatalf("Emit: %v (R4: nil/empty sink list is a no-op)", err)
			}
		})
	}
}

// TestFanOutErrorWarnsAndContinues proves R4 fail-open: a failing sink is
// WARN-logged, the caller still gets no error and later sinks still run.
func TestFanOutErrorWarnsAndContinues(t *testing.T) {
	log, buf := captureLogger(t)
	failing := &recordingSink{err: errors.New("boom")}
	next := &recordingSink{}
	fo := NewFanOut(log, failing, next)

	ev := ports.SignalEvent{Action: "auth.login", Severity: domain.SeverityInfo}
	if err := fo.Emit(context.Background(), ev); err != nil {
		t.Fatalf("Emit: %v (caller never fails on sink errors)", err)
	}
	if len(next.events) != 1 {
		t.Fatalf("later sink received %d events, want 1 (remaining sinks still run)", len(next.events))
	}
	if out := buf.String(); !strings.Contains(out, "breach sink failed") {
		t.Fatalf("fan-out must WARN the sink error:\n%s", out)
	}
}

// TestFanOutPanicRecoveredAndContinues proves R4: a panicking sink is recovered,
// WARN-logged, and later sinks still run.
func TestFanOutPanicRecoveredAndContinues(t *testing.T) {
	log, buf := captureLogger(t)
	panicking := &recordingSink{panic: true}
	next := &recordingSink{}
	fo := NewFanOut(log, panicking, next)

	if err := fo.Emit(context.Background(), ports.SignalEvent{Action: "auth.login"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(next.events) != 1 {
		t.Fatalf("later sink received %d events, want 1 (panic must not stop the fan-out)", len(next.events))
	}
	if out := buf.String(); !strings.Contains(out, "breach sink panic") {
		t.Fatalf("fan-out must WARN the recovered panic:\n%s", out)
	}
}

// TestFanOutOrderMatchesRegistration proves sinks run in registration order —
// slog first, audit second is the composition-root contract (R2).
func TestFanOutOrderMatchesRegistration(t *testing.T) {
	var rec []ports.SignalEvent
	first := &orderSink{rec: &rec}
	second := &orderSink{rec: &rec}
	fo := NewFanOut(discardLogger(), first, second)

	ev := ports.SignalEvent{Action: "lot.create", Severity: domain.SeverityInfo}
	if err := fo.Emit(context.Background(), ev); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(rec) != 2 {
		t.Fatalf("shared recorder calls = %d, want 2 (both sinks ran in registration order)", len(rec))
	}
	if rec[0] != ev || rec[1] != ev {
		t.Fatalf("calls = %+v, want the same event delivered to each sink", rec)
	}
}
