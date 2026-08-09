// Package services — breach detection: a pure, table-driven classifier that
// maps security-relevant signals to severitized events, plus the emission
// side effect (slog always, audit row only for tenant-context events).
package services

import (
	"context"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// Signal is the normalized security-relevant event the system can observe.
// It is intentionally coarse: the classifier decides severity + audit policy.
type Signal string

const (
	// SignalLoginSuccess: a user logged in successfully (info event).
	SignalLoginSuccess Signal = "login_success"
	// SignalRefreshSuccess: a refresh token was rotated successfully (info).
	SignalRefreshSuccess Signal = "refresh_success"
	// SignalRefreshReplay: a refresh token that was already consumed is used
	// again — possible token theft.
	SignalRefreshReplay Signal = "refresh_replay"
	// SignalRateLimitExceeded: a caller exceeded a per-route limit — possible
	// brute force / credential stuffing.
	SignalRateLimitExceeded Signal = "rate_limit_exceeded"
	// SignalLoginFailed: an authentication attempt failed (bad credentials).
	SignalLoginFailed Signal = "login_failed"
	// SignalCrossTenant: a cross-tenant probe was observed (RLS prevented the
	// leak; the event is logged but never persisted as an audit row).
	SignalCrossTenant Signal = "cross_tenant_probe"
	// SignalExpiredTokenReplay: an already-expired token is replayed. This is
	// a NORMAL flow (clients retry with stale tokens), so it produces NO event.
	SignalExpiredTokenReplay Signal = "expired_token_replay"
)

// Event is the classifier output: the action to record, its severity and
// whether it should become an audit row (only tenant-context events).
type Event struct {
	Action    string
	Severity  string
	EmitAudit bool
}

// Detect classifies a signal. anonymous means the request had no tenant
// identity (e.g. an unauthenticated rate-limit hit or login failure): the
// event is still logged via slog but never written to the audit table —
// audit rows require a tenant context under RLS.
func Detect(signal Signal, anonymous bool) *Event {
	e, ok := breachTable[signal]
	if !ok {
		return nil
	}
	ev := e
	if anonymous {
		ev.EmitAudit = false
	}
	return &ev
}

// breachTable is the single source of truth for signal -> event mapping.
var breachTable = map[Signal]Event{
	SignalLoginSuccess: {
		Action:    "auth.login",
		Severity:  domain.SeverityInfo,
		EmitAudit: true,
	},
	SignalRefreshSuccess: {
		Action:    "auth.refresh",
		Severity:  domain.SeverityInfo,
		EmitAudit: true,
	},
	SignalRefreshReplay: {
		Action:    "auth.refresh.replay",
		Severity:  domain.SeverityCritical,
		EmitAudit: true,
	},
	SignalRateLimitExceeded: {
		Action:    "security.rate_limit.exceeded",
		Severity:  domain.SeverityWarn,
		EmitAudit: true,
	},
	SignalLoginFailed: {
		Action:    "auth.login.failed",
		Severity:  domain.SeverityInfo,
		EmitAudit: true,
	},
	SignalCrossTenant: {
		Action:    "security.cross_tenant_probe",
		Severity:  domain.SeverityWarn,
		EmitAudit: false,
	},
	// SignalExpiredTokenReplay intentionally absent: no event.
}

// EmitSignal is the exported emit seam every caller (services and middleware)
// uses: it classifies the signal, maps the result onto the port payload and
// hands it to the sink. Detect runs inside so every caller gets the same
// classification. A nil sink is a no-op; sink failures are informational
// (fail-open, R4).
func EmitSignal(ctx context.Context, sink ports.BreachSignalSink, signal Signal, anonymous bool, tenantID, actorID, requestID string) {
	emitEvent(ctx, sink, tenantID, actorID, signal, anonymous, Detect(signal, anonymous), requestID)
}

// emitEvent is the internal single emission path: an already-classified Event
// is mapped onto the port payload and delivered to the sink. A nil sink or nil
// event is a no-op. The signal is informational (e.g. "" for the lot.create
// raw event); the action drives the audit row.
func emitEvent(ctx context.Context, sink ports.BreachSignalSink, tenantID, actorID string, signal Signal, anonymous bool, ev *Event, requestID string) {
	if ev == nil || sink == nil {
		return
	}
	_ = sink.Emit(ctx, ports.SignalEvent{
		Signal:    string(signal),
		Action:    ev.Action,
		Severity:  ev.Severity,
		TenantID:  tenantID,
		ActorID:   actorID,
		RequestID: requestID,
		Anonymous: anonymous,
	})
}
