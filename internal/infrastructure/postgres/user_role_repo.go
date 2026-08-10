package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// UserRoleRepo is the pgx implementation of ports.UserRoleRepository.
//
// Every query runs inside WithTenant, which binds app.tenant_id for the
// lifetime of one transaction, so Postgres Row-Level Security — not a WHERE
// clause — is what isolates memberships between tenants (the user_repo
// pattern). The INSERT mirrors cmd/seed/main.go's assignRole.
type UserRoleRepo struct {
	pool *pgxpool.Pool
}

// NewUserRoleRepo wires a pool into the repository.
func NewUserRoleRepo(pool *pgxpool.Pool) *UserRoleRepo {
	return &UserRoleRepo{pool: pool}
}

// Compile-time contract: UserRoleRepo satisfies ports.UserRoleRepository.
var _ ports.UserRoleRepository = (*UserRoleRepo)(nil)

// Assign inserts a role membership inside the tenant context bound from
// role.TenantID. The composite PK (user_id, role_code) rejects a duplicate
// pair; mapPgErr surfaces the 23505 unique_violation as domain.ErrConflict.
func (r *UserRoleRepo) Assign(ctx context.Context, role *domain.UserRole) error {
	return WithTenant(ctx, r.pool, role.TenantID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO app.user_roles (user_id, role_code, tenant_id) VALUES ($1, $2, $3)`,
			role.UserID, role.RoleCode, role.TenantID)
		if err != nil {
			return mapPgErr("insert user role", err)
		}
		return nil
	})
}

// ListByUser returns the user's memberships in the tenant, ordered by
// role_code for deterministic output. RLS hides other tenants' rows entirely.
func (r *UserRoleRepo) ListByUser(ctx context.Context, tenantID, userID string) ([]*domain.UserRole, error) {
	var roles []*domain.UserRole
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		rows, err := tx.Query(ctx,
			`SELECT user_id, role_code, tenant_id FROM app.user_roles WHERE user_id = $1 ORDER BY role_code`, userID)
		if err != nil {
			return fmt.Errorf("list user roles: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var role domain.UserRole
			if err := rows.Scan(&role.UserID, &role.RoleCode, &role.TenantID); err != nil {
				return fmt.Errorf("scan user role: %w", err)
			}
			roles = append(roles, &role)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return roles, nil
}
