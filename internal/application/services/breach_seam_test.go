// Package services — emit seam tests (M30 breach-signal substrate, PR1).
// They pin services.EmitSignal and the internal emitEvent: Detect runs inside,
// nil sink / unknown signal are no-ops, and the lot.create raw event maps onto
// the port payload with Signal="".
package services

import (
	"context"
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// TestEmitSignalDetectInside proves the exported seam classifies the signal
// (Detect inside) and delivers the full payload to the sink.
func TestEmitSignalDetectInside(t *testing.T) {
	sink := &recordingSink{}
	EmitSignal(context.Background(), sink, SignalRefreshReplay, false, "tenant-1", "user-1", "req-42")
	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	want := ports.SignalEvent{
		Signal: "refresh_replay", Action: "auth.refresh.replay", Severity: domain.SeverityCritical,
		TenantID: "tenant-1", ActorID: "user-1", RequestID: "req-42",
	}
	if sink.events[0] != want {
		t.Fatalf("event = %+v, want %+v", sink.events[0], want)
	}
}

// TestEmitSignalAnonymousSetsFlag proves the anonymous flag reaches the payload
// so the audit sink can skip the row (RLS).
func TestEmitSignalAnonymousSetsFlag(t *testing.T) {
	sink := &recordingSink{}
	EmitSignal(context.Background(), sink, SignalLoginSuccess, true, "tenant-1", "user-1", "")
	if len(sink.events) != 1 || !sink.events[0].Anonymous {
		t.Fatalf("event = %+v, want exactly one event with Anonymous=true", sink.events)
	}
}

// TestEmitSignalNilSinkNoOp proves R4: a nil sink is a no-op and the caller
// completes without error or panic.
func TestEmitSignalNilSinkNoOp(t *testing.T) {
	EmitSignal(context.Background(), nil, SignalLoginSuccess, false, "tenant-1", "user-1", "req-1")
}

// TestEmitSignalUnknownSignalNoOp proves a signal absent from breachTable
// (SignalExpiredTokenReplay) produces no event — Detect returns nil.
func TestEmitSignalUnknownSignalNoOp(t *testing.T) {
	sink := &recordingSink{}
	EmitSignal(context.Background(), sink, SignalExpiredTokenReplay, false, "tenant-1", "user-1", "req-1")
	if len(sink.events) != 0 {
		t.Fatalf("events = %d, want 0 (SignalExpiredTokenReplay has no breachTable entry)", len(sink.events))
	}
}

// TestEmitEventSeamLotCreate proves the internal seam delivers the raw lot.create
// event (Signal="", Action carries the audit action) with the actor.
func TestEmitEventSeamLotCreate(t *testing.T) {
	sink := &recordingSink{}
	emitEvent(context.Background(), sink, "tenant-1", "actor-1", "", false,
		&Event{Action: "lot.create", Severity: domain.SeverityInfo, EmitAudit: true}, "req-3")
	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	want := ports.SignalEvent{
		Signal: "", Action: "lot.create", Severity: domain.SeverityInfo,
		TenantID: "tenant-1", ActorID: "actor-1", RequestID: "req-3",
	}
	if sink.events[0] != want {
		t.Fatalf("event = %+v, want %+v", sink.events[0], want)
	}
}

// TestEmitEventSeamNilGuards proves nil sink and nil event are no-ops.
func TestEmitEventSeamNilGuards(t *testing.T) {
	sink := &recordingSink{}
	emitEvent(context.Background(), nil, "t", "a", "", false, &Event{Action: "x"}, "")
	emitEvent(context.Background(), sink, "t", "a", "", false, nil, "")
	if len(sink.events) != 0 {
		t.Fatalf("events = %d, want 0 (nil sink / nil event are no-ops)", len(sink.events))
	}
}
