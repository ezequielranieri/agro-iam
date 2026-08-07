package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// LotRepo is the pgx implementation of ports.LotRepository.
//
// Every query runs inside WithTenant, which binds app.tenant_id for the
// lifetime of one transaction, so Postgres Row-Level Security â€” not the WHERE
// clause â€” is what actually isolates tenants. The explicit tenant_id filter
// stays as defense-in-depth: even if RLS were misconfigured, application code
// cannot accidentally read or write another tenant's rows.
type LotRepo struct {
	pool *pgxpool.Pool
}

func NewLotRepo(pool *pgxpool.Pool) *LotRepo {
	return &LotRepo{pool: pool}
}

const lotColumns = `id, tenant_id, name, area_ha, crop, created_at, updated_at`

func scanLot(row pgx.Row) (*domain.Lot, error) {
	var l domain.Lot
	if err := row.Scan(&l.ID, &l.TenantID, &l.Name, &l.AreaHA, &l.Crop, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, err
	}
	return &l, nil
}

func (r *LotRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Lot, error) {
	var lot *domain.Lot
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+lotColumns+` FROM app.lots WHERE id = $1 AND tenant_id = $2`, id, tenantID)
		l, err := scanLot(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query lot by id: %w", err)
		}
		lot = l
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lot, nil
}

func (r *LotRepo) List(ctx context.Context, tenantID string) ([]*domain.Lot, error) {
	var lots []*domain.Lot
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+lotColumns+` FROM app.lots WHERE tenant_id = $1 ORDER BY name`, tenantID)
		if err != nil {
			return fmt.Errorf("list lots: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			l, err := scanLot(rows)
			if err != nil {
				return fmt.Errorf("scan lot: %w", err)
			}
			lots = append(lots, l)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return lots, nil
}

func (r *LotRepo) Create(ctx context.Context, lot *domain.Lot) error {
	// The RLS tenant context is bound from lot.TenantID: the lot itself carries
	// the tenant it belongs to, so the insert can only succeed inside that
	// tenant's RLS policy. Callers must set lot.TenantID before calling Create.
	return WithTenant(ctx, r.pool, lot.TenantID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO app.lots (id, tenant_id, name, area_ha, crop)
			 VALUES ($1, $2, $3, $4, $5)`,
			lot.ID, lot.TenantID, lot.Name, lot.AreaHA, lot.Crop)
		if err != nil {
			return mapPgErr("insert lot", err)
		}
		return nil
	})
}
