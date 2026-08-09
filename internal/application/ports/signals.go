package ports

import "context"

// SignalEvent is the normalized breach-signal payload delivered to every sink.
// It is the outbound port shape: consumers (slog, audit today; alerting later)
// only see this value and never the classifier internals.
type SignalEvent struct {
	Signal, Action, Severity, TenantID, ActorID, RequestID string
	Anonymous                                              bool
}

// BreachSignalSink consumes breach signals. The classifier and the emit sites
// depend on this interface (DIP); the composition root registers the concrete
// sinks (slog first, then audit).
type BreachSignalSink interface {
	// Emit delivers the event. Errors are informational: consumers decide
	// handling (fail-open), and the caller never fails because of a sink.
	Emit(ctx context.Context, ev SignalEvent) error
}
