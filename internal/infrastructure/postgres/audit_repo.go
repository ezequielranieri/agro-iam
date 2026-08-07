package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// AuditRepo is the pgx implementation of ports.AuditRepository. It is
// append-only: there is no update or delete path by design.
type AuditRepo struct {
	pool *pgxpool.Pool
}

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

func (r *AuditRepo) Append(ctx context.Context, entry *domain.AuditEntry) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO app.audit_log (tenant_id, actor_user_id, action, entity_type, entity_id, payload)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		entry.TenantID, entry.ActorUserID, entry.Action, entry.EntityType,
		entry.EntityID, entry.Payload)
	if err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}
	return nil
}
