package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// TenantRepo is the pgx implementation of ports.TenantRepository. Tenants form
// the public registry and are therefore NOT RLS-protected.
type TenantRepo struct {
	pool *pgxpool.Pool
}

func NewTenantRepo(pool *pgxpool.Pool) *TenantRepo {
	return &TenantRepo{pool: pool}
}

func (r *TenantRepo) FindByID(ctx context.Context, id string) (*domain.Tenant, error) {
	var t domain.Tenant
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, created_at, updated_at FROM app.tenants WHERE id = $1`, id,
	).Scan(&t.ID, &t.Name, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query tenant by id: %w", err)
	}
	return &t, nil
}

func (r *TenantRepo) Create(ctx context.Context, tenant *domain.Tenant) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO app.tenants (id, name) VALUES ($1, $2)`,
		tenant.ID, tenant.Name)
	if err != nil {
		return mapPgErr("insert tenant", err)
	}
	return nil
}

// List returns every tenant of the global registry. Tenants carry no RLS by
// design — the realm list is public and credentials-free (AP2) — and the ids
// are read at request time, so the demo login screen never hardcodes a uuid
// and survives reseeds.
func (r *TenantRepo) List(ctx context.Context) ([]*domain.Tenant, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, created_at, updated_at FROM app.tenants ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*domain.Tenant
	for rows.Next() {
		var t domain.Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan tenant: %w", err)
		}
		tenants = append(tenants, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tenants rows: %w", err)
	}
	return tenants, nil
}
