package services

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// auditService implements ports.AuditService. It owns the chaining rules (tail
// read, seq/prev/chain computation, the 23505 retry and fail-open logging) and
// delegates persistence to a ports.AuditRepository; RLS enforcement is the
// repository's concern, not the service's (pattern: lotService).
type auditService struct {
	repo ports.AuditRepository
	log  *slog.Logger
	now  func() time.Time
}

// NewAuditService wires the repository into an AuditService. The clock is
// injectable for deterministic tests; pass time.Now when unsure.
func NewAuditService(repo ports.AuditRepository, log *slog.Logger) ports.AuditService {
	return &auditService{repo: repo, log: log, now: time.Now}
}

// Record appends a chained entry (spec R9). It runs synchronously in its own
// WithTenant transaction: the repository reads the tail with FOR UPDATE, the
// service links the new entry (seq, prev_hash, chain_hash), and the append
// inserts. A UNIQUE(tenant_id, seq) conflict — a lost concurrent-append race —
// is retried once with a fresh tail (spec R7); a persistent failure is
// WARN-logged and the error returned so the caller can proceed (fail-open).
func (s *auditService) Record(ctx context.Context, tenantID, actorUserID, action, entityType, entityID string,
	payload []byte, severity string) error {
	if tenantID == "" {
		return domain.ErrTenantRequired
	}
	if severity == "" {
		severity = domain.SeverityInfo
	}

	// Up to two attempts: the first may lose the race, the second re-reads the
	// tail and re-numbers, which resolves it (spec R7).
	for attempt := 0; attempt < 2; attempt++ {
		tail, err := s.repo.Tail(ctx, tenantID)
		if err != nil {
			s.log.Warn("audit tail failed", "tenant_id", tenantID, "action", action, "error", err)
			return err
		}

		seq := int64(1)
		prev := genesisPrevHash
		if tail != nil {
			seq = tail.Seq + 1
			prev = tail.ChainHash
		}

		entry := &domain.AuditEntry{
			TenantID:    tenantID,
			ActorUserID: actorUserID,
			Action:      action,
			EntityType:  entityType,
			EntityID:    entityID,
			Payload:     payload,
			// Microsecond truncation matches Postgres timestamptz resolution:
			// the value must round-trip through the database bit-identically or
			// verification would report a false tamper.
			CreatedAt: s.now().UTC().Truncate(time.Microsecond),
			Seq:       seq,
			PrevHash:  prev,
			Severity:  severity,
		}
		canon, err := CanonicalizeEntry(payload)
		if err != nil {
			s.log.Warn("audit payload canonicalization failed", "tenant_id", tenantID, "action", action, "error", err)
			return err
		}
		entry.ChainHash = HashChainEntry(prev, seq, *entry, canon)

		if err := s.repo.Append(ctx, entry); err != nil {
			if !errors.Is(err, domain.ErrConflict) {
				// Any non-conflict failure is logged and surfaced; the caller
				// still proceeds with the main flow (fail-open).
				s.log.Warn("audit append failed", "tenant_id", tenantID, "action", action, "error", err)
				return err
			}
			// 23505 — a concurrent append won the seq. Retry once.
			continue
		}
		return nil
	}

	s.log.Warn("audit append conflict after retry", "tenant_id", tenantID, "action", action)
	return domain.ErrConflict
}

// VerifyChain recomputes the tenant's chain from every stored row ordered by
// seq and returns the first broken seq, or 0 when intact (spec R8). Internal
// only — no public endpoint.
func (s *auditService) VerifyChain(ctx context.Context, tenantID string) (int64, error) {
	if tenantID == "" {
		return 0, domain.ErrTenantRequired
	}
	entries, err := s.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	return verifyChainEntries(entries)
}

// Latest returns the tenant's most recent entries, newest first (seq DESC).
// limit bounds the result — the demo audit screen uses a latest-100 window
// (AP1). RLS enforcement is the repository's concern: the query runs inside
// WithTenant, so a tenant can never read another's trail.
func (s *auditService) Latest(ctx context.Context, tenantID string, limit int) ([]*domain.AuditEntry, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}
	if limit <= 0 {
		return nil, domain.ErrInvalidInput
	}
	return s.repo.ListRecent(ctx, tenantID, limit)
}

var _ ports.AuditService = (*auditService)(nil)
