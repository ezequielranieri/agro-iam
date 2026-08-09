package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// SlogSink renders breach signals to the structured logger with exact slice-3
// parity: message "security" and attrs action/severity/tenant/actor/request_id,
// severity→level (critical→Error, warn→Warn, else Info). Anonymous events are
// still logged (slog-always, DECISIONS 2.15) — the slog sink never drops them.
type SlogSink struct {
	log *slog.Logger
}

// NewSlogSink builds a slog adapter. A nil log is discarded (matches the
// services' constructor convention).
func NewSlogSink(log *slog.Logger) *SlogSink {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &SlogSink{log: log}
}

// Emit writes the "security" line at the level mapped from the severity.
func (s *SlogSink) Emit(ctx context.Context, ev ports.SignalEvent) error {
	attrs := []any{
		"action", ev.Action,
		"severity", ev.Severity,
		"tenant", ev.TenantID,
		"actor", ev.ActorID,
		"request_id", ev.RequestID,
	}
	switch ev.Severity {
	case domain.SeverityCritical:
		s.log.Error("security", attrs...)
	case domain.SeverityWarn:
		s.log.Warn("security", attrs...)
	default:
		s.log.Info("security", attrs...)
	}
	return nil
}

// AuditSink persists tenant-context breach signals as audit rows. RLS-safe:
// anonymous or tenantless events are slog-only (no tenant owns the row). It is
// fail-open — a nil audit service or a Record error never fails the caller.
type AuditSink struct {
	audit ports.AuditService
	log   *slog.Logger
}

// NewAuditSink builds the audit adapter. audit may be nil (fail-open with no
// backend); a nil log is discarded.
func NewAuditSink(audit ports.AuditService, log *slog.Logger) *AuditSink {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &AuditSink{audit: audit, log: log}
}

// Emit records the row unless the event is anonymous or tenantless. A Record
// failure is WARN-logged with the same message slice 3 used, and swallowed —
// the caller always proceeds (fail-open, R5 parity).
func (s *AuditSink) Emit(ctx context.Context, ev ports.SignalEvent) error {
	if ev.Anonymous || ev.TenantID == "" {
		return nil
	}
	if s.audit == nil {
		return nil
	}
	if err := s.audit.Record(ctx, ev.TenantID, ev.ActorID, ev.Action, "security", ev.Action, nil, ev.Severity); err != nil {
		s.log.Warn("audit emission failed (fail-open)", "action", ev.Action, "error", err)
	}
	return nil
}

// fanOut is a BreachSignalSink that composes several sinks in registration
// order. An empty/nil sink list is a no-op; a per-sink error is WARN-logged and
// the remaining sinks still run; a panicking sink is recovered so the others
// complete too (R4 — the caller never fails because of a sink).
type fanOut struct {
	log   *slog.Logger
	sinks []ports.BreachSignalSink
}

// NewFanOut composes sinks into a single BreachSignalSink. The composition
// root registers slog FIRST, then audit (R2). A nil log is discarded.
func NewFanOut(log *slog.Logger, sinks ...ports.BreachSignalSink) ports.BreachSignalSink {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &fanOut{log: log, sinks: sinks}
}

// Emit runs every non-nil sink in registration order, WARN-logging per-sink
// errors and recovering panics. It always returns nil: failures are
// informational (fail-open).
func (f *fanOut) Emit(ctx context.Context, ev ports.SignalEvent) error {
	for _, sink := range f.sinks {
		if sink == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					f.log.Warn("breach sink panic recovered", "sink", fmt.Sprintf("%T", sink), "panic", r)
				}
			}()
			if err := sink.Emit(ctx, ev); err != nil {
				f.log.Warn("breach sink failed (fail-open)", "sink", fmt.Sprintf("%T", sink), "error", err)
			}
		}()
	}
	return nil
}

var _ ports.BreachSignalSink = (*SlogSink)(nil)
var _ ports.BreachSignalSink = (*AuditSink)(nil)
