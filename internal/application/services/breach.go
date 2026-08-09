// Package services — breach detection: a pure, table-driven classifier that
// maps security-relevant signals to severitized events, plus the emission
// side effect (slog always, audit row only for tenant-context events).
package services

import (
	"context"
	"log/slog"

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

// EmitEvent applies the classifier output: slog always; audit row only when
// the event says so and the caller is NOT anonymous. A nil audit service
// fails open with a WARN, matching the audit fail-open contract.
func EmitEvent(ctx context.Context, log *slog.Logger, audit ports.AuditService, tenantID, actorID string, ev *Event, requestID string) {
	if ev == nil {
		return
	}

	attrs := []any{
		"action", ev.Action,
		"severity", ev.Severity,
		"tenant", tenantID,
		"actor", actorID,
		"request_id", requestID,
	}
	switch ev.Severity {
	case domain.SeverityCritical:
		log.Error("security", attrs...)
	case domain.SeverityWarn:
		log.Warn("security", attrs...)
	default:
		log.Info("security", attrs...)
	}

	if !ev.EmitAudit || audit == nil || tenantID == "" {
		return
	}
	if err := audit.Record(ctx, tenantID, actorID, ev.Action, "security", ev.Action, nil, ev.Severity); err != nil {
		log.Warn("audit emission failed (fail-open)", "action", ev.Action, "error", err)
	}
}
