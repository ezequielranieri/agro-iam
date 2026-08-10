package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// ApplicationRepo is the pgx implementation of ports.ApplicationRepository.
//
// Every query runs inside WithTenant, which binds app.tenant_id for the
// lifetime of one transaction, so Postgres Row-Level Security — not a WHERE
// clause — is what isolates tenants (the user_repo pattern). The explicit
// tenant_id filter is intentionally absent: RLS is the sole enforcement point,
// and there are no raw pool queries.
type ApplicationRepo struct {
	pool *pgxpool.Pool
}

func NewApplicationRepo(pool *pgxpool.Pool) *ApplicationRepo {
	return &ApplicationRepo{pool: pool}
}

// Compile-time contract: ApplicationRepo satisfies ports.ApplicationRepository.
var _ ports.ApplicationRepository = (*ApplicationRepo)(nil)

const applicationColumns = `id, tenant_id, lot_id, campaign_id, product_name, dose, applied_at, operator_id, notes, created_at, updated_at`

func scanApplication(row pgx.Row) (*domain.Application, error) {
	var a domain.Application
	// operator_id is nullable: scan into a *string and map NULL back to the
	// domain's "" zero value (pgx refuses to scan NULL into a plain string).
	var operatorID *string
	if err := row.Scan(
		&a.ID, &a.TenantID, &a.LotID, &a.CampaignID, &a.ProductName, &a.Dose,
		&a.AppliedAt, &operatorID, &a.Notes, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if operatorID != nil {
		a.OperatorID = *operatorID
	}
	return &a, nil
}

func (r *ApplicationRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Application, error) {
	var app *domain.Application
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+applicationColumns+` FROM app.applications WHERE id = $1`, id)
		found, err := scanApplication(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query application by id: %w", err)
		}
		app = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return app, nil
}

func (r *ApplicationRepo) List(ctx context.Context, tenantID string) ([]*domain.Application, error) {
	var apps []*domain.Application
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+applicationColumns+` FROM app.applications ORDER BY applied_at DESC`)
		if err != nil {
			return fmt.Errorf("list applications: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			a, err := scanApplication(rows)
			if err != nil {
				return fmt.Errorf("scan application: %w", err)
			}
			apps = append(apps, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return apps, nil
}

func (r *ApplicationRepo) Create(ctx context.Context, app *domain.Application) error {
	// The RLS tenant context is bound from app.TenantID: the INSERT can only
	// succeed inside that tenant's policy (WITH CHECK).
	return WithTenant(ctx, r.pool, app.TenantID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO app.applications (id, tenant_id, lot_id, campaign_id, product_name, dose, applied_at, operator_id, notes)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			app.ID, app.TenantID, app.LotID, app.CampaignID, app.ProductName,
			app.Dose, app.AppliedAt, nullableUUID(app.OperatorID), app.Notes)
		if err != nil {
			return mapPgErr("insert application", err)
		}
		return nil
	})
}

// Update is a full-row replace (UserRepo.Update style): every mutable column is
// rewritten, no partial PATCH semantics. RowsAffected()==0 means the row does
// not exist or is hidden by RLS (owned by another tenant), surfaced as
// domain.ErrNotFound.
func (r *ApplicationRepo) Update(ctx context.Context, app *domain.Application) error {
	return WithTenant(ctx, r.pool, app.TenantID, func(tx pgxTx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE app.applications
			 SET lot_id = $2, campaign_id = $3, product_name = $4, dose = $5,
			     applied_at = $6, operator_id = $7, notes = $8, updated_at = now()
			 WHERE id = $1`,
			app.ID, app.LotID, app.CampaignID, app.ProductName, app.Dose,
			app.AppliedAt, nullableUUID(app.OperatorID), app.Notes)
		if err != nil {
			return mapPgErr("update application", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

func (r *ApplicationRepo) Delete(ctx context.Context, tenantID, id string) error {
	return WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM app.applications WHERE id = $1`, id)
		if err != nil {
			return mapPgErr("delete application", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

// nullableUUID maps the domain's "" (no value) to a NULL bind parameter. The
// operator_id column is a nullable uuid: binding an empty string would fail
// with "invalid input syntax for type uuid".
func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
