// Package services — breach detection tests (PR3-T1 RED phase).
// They define the Detect() table-driven classifier before it exists.
package services

import (
	"testing"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// TestDetectTableDriven proves the pure classifier maps every signal + tenant
// context to the expected event, and that anonymous events never emit audit
// rows.
func TestDetectTableDriven(t *testing.T) {
	cases := []struct {
		label     string
		signal    Signal
		anonymous bool
		wantEvent Event
		wantNil   bool // true when no event should be produced at all
	}{
		{
			label:     "refresh token replay (tenant context) -> critical audit row",
			signal:    SignalRefreshReplay,
			anonymous: false,
			wantEvent: Event{
				Action:    "auth.refresh.replay",
				Severity:  domain.SeverityCritical,
				EmitAudit: true,
			},
		},
		{
			label:     "rate limit exceeded -> warn audit row",
			signal:    SignalRateLimitExceeded,
			anonymous: false,
			wantEvent: Event{
				Action:    "security.rate_limit.exceeded",
				Severity:  domain.SeverityWarn,
				EmitAudit: true,
			},
		},
		{
			label:     "login failed -> info audit row",
			signal:    SignalLoginFailed,
			anonymous: false,
			wantEvent: Event{
				Action:    "auth.login.failed",
				Severity:  domain.SeverityInfo,
				EmitAudit: true,
			},
		},
		{
			label:     "cross-tenant probe -> no audit row (never persisted)",
			signal:    SignalCrossTenant,
			anonymous: false,
			wantEvent: Event{
				Action:    "security.cross_tenant_probe",
				Severity:  domain.SeverityWarn,
				EmitAudit: false,
			},
		},
		{
			label:     "anonymous refresh replay -> critical but slog only, no row",
			signal:    SignalRefreshReplay,
			anonymous: true,
			wantEvent: Event{
				Action:    "auth.refresh.replay",
				Severity:  domain.SeverityCritical,
				EmitAudit: false,
			},
		},
		{
			label:     "anonymous login failed -> info but slog only, no row",
			signal:    SignalLoginFailed,
			anonymous: true,
			wantEvent: Event{
				Action:    "auth.login.failed",
				Severity:  domain.SeverityInfo,
				EmitAudit: false,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			got := Detect(c.signal, c.anonymous)
			if c.wantNil {
				if got != nil {
					t.Fatalf("Detect(%v, %v) = %+v, want nil", c.signal, c.anonymous, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("Detect(%v, %v) = nil, want %+v", c.signal, c.anonymous, c.wantEvent)
			}
			if got.Action != c.wantEvent.Action {
				t.Errorf("Action = %q, want %q", got.Action, c.wantEvent.Action)
			}
			if got.Severity != c.wantEvent.Severity {
				t.Errorf("Severity = %q, want %q", got.Severity, c.wantEvent.Severity)
			}
			if got.EmitAudit != c.wantEvent.EmitAudit {
				t.Errorf("EmitAudit = %v, want %v", got.EmitAudit, c.wantEvent.EmitAudit)
			}
		})
	}
}

// TestDetectExpiredTokenReplayProducesNoEvent proves an expired-token replay is
// not a signal: it is a normal expired-token flow, not a breach.
func TestDetectExpiredTokenReplayProducesNoEvent(t *testing.T) {
	if got := Detect(SignalExpiredTokenReplay, false); got != nil {
		t.Fatalf("Detect(SignalExpiredTokenReplay) = %+v, want nil", got)
	}
	if got := Detect(SignalExpiredTokenReplay, true); got != nil {
		t.Fatalf("Detect(SignalExpiredTokenReplay, anonymous) = %+v, want nil", got)
	}
}
