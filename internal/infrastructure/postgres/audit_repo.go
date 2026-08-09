package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// AuditRepo is the pgx implementation of ports.AuditRepository. It is
// append-only: there is no update or delete path by design.
//
// Every query runs inside WithTenant, which binds app.tenant_id for the
// lifetime of one transaction, so Postgres Row-Level Security — not the WHERE
// clause — is what actually isolates tenants. An append without tenant context
// is rejected outright by FORCE RLS (zero rows, never a silent cross-tenant
// write). Empty actor/entity id values are stored as SQL NULL via NULLIF.
type AuditRepo struct {
	pool *pgxpool.Pool
}

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

func (r *AuditRepo) Append(ctx context.Context, entry *domain.AuditEntry) error {
	// The RLS tenant context is bound from entry.TenantID: the entry itself
	// carries the tenant it belongs to, so the insert can only succeed inside
	// that tenant's RLS policy. NULLIF maps empty strings to SQL NULL (spec
	// R2) so actor_user_id / entity_id never store empty strings.
	return WithTenant(ctx, r.pool, entry.TenantID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO app.audit_log
				(tenant_id, actor_user_id, action, entity_type, entity_id, payload,
				 seq, prev_hash, chain_hash, severity, created_at)
			 VALUES ($1, NULLIF($2, '')::uuid, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11)`,
			entry.TenantID, entry.ActorUserID, entry.Action, entry.EntityType,
			entry.EntityID, entry.Payload, entry.Seq, entry.PrevHash, entry.ChainHash,
			entry.Severity, entry.CreatedAt)
		if err != nil {
			// 23505 unique_violation (UNIQUE tenant_id, seq) surfaces as
			// domain.ErrConflict so the service can retry the race.
			return mapPgErr("insert audit entry", err)
		}
		return nil
	})
}

// Tail returns the newest chained entry (seq + chain_hash) for the tenant, or
// nil when the tenant has none. FOR UPDATE narrows concurrent-appends races:
// concurrent tails of the same tenant serialize on the newest row, so the
// UNIQUE(tenant_id, seq) retry in the service is the belt-and-suspenders for
// the genesis race (empty table, no row to lock).
func (r *AuditRepo) Tail(ctx context.Context, tenantID string) (*domain.AuditEntry, error) {
	var tail *domain.AuditEntry
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		var e domain.AuditEntry
		err := tx.QueryRow(ctx,
			`SELECT seq, chain_hash FROM app.audit_log
			 ORDER BY seq DESC LIMIT 1 FOR UPDATE`).Scan(&e.Seq, &e.ChainHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // no rows yet — genesis
		}
		if err != nil {
			return fmt.Errorf("query audit tail: %w", err)
		}
		tail = &e
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tail, nil
}

// ListByTenant returns every entry of the tenant ordered by seq ascending,
// used by chain verification. NULL actor/entity/payload read back as empty
// string / JSON null so the verifier recomputes the same canonical bytes that
// were hashed at insert time.
func (r *AuditRepo) ListByTenant(ctx context.Context, tenantID string) ([]*domain.AuditEntry, error) {
	var entries []*domain.AuditEntry
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, tenant_id,
			        COALESCE(actor_user_id::text, ''),
			        action, entity_type,
			        COALESCE(entity_id, ''),
			        COALESCE(payload, 'null'::jsonb),
			        created_at, seq, prev_hash, chain_hash, severity
			 FROM app.audit_log ORDER BY seq ASC`)
		if err != nil {
			return fmt.Errorf("list audit entries: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var e domain.AuditEntry
			if err := rows.Scan(&e.ID, &e.TenantID, &e.ActorUserID, &e.Action,
				&e.EntityType, &e.EntityID, &e.Payload, &e.CreatedAt,
				&e.Seq, &e.PrevHash, &e.ChainHash, &e.Severity); err != nil {
				return fmt.Errorf("scan audit entry: %w", err)
			}
			entries = append(entries, &e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}
