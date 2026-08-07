package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// ApplicationRepo is the pgx implementation of ports.ApplicationRepository.
type ApplicationRepo struct {
	pool *pgxpool.Pool
}

func NewApplicationRepo(pool *pgxpool.Pool) *ApplicationRepo {
	return &ApplicationRepo{pool: pool}
}

const applicationColumns = `id, tenant_id, lot_id, campaign_id, product_name, dose, applied_at, operator_id, notes, created_at, updated_at`

func scanApplication(row pgx.Row) (*domain.Application, error) {
	var a domain.Application
	if err := row.Scan(
		&a.ID, &a.TenantID, &a.LotID, &a.CampaignID, &a.ProductName, &a.Dose,
		&a.AppliedAt, &a.OperatorID, &a.Notes, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *ApplicationRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Application, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+applicationColumns+` FROM app.applications WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	a, err := scanApplication(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query application by id: %w", err)
	}
	return a, nil
}

func (r *ApplicationRepo) List(ctx context.Context, tenantID string) ([]*domain.Application, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+applicationColumns+` FROM app.applications WHERE tenant_id = $1 ORDER BY applied_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	defer rows.Close()

	var apps []*domain.Application
	for rows.Next() {
		a, err := scanApplication(rows)
		if err != nil {
			return nil, fmt.Errorf("scan application: %w", err)
		}
		apps = append(apps, a)
	}
	return apps, rows.Err()
}

func (r *ApplicationRepo) Create(ctx context.Context, app *domain.Application) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO app.applications (id, tenant_id, lot_id, campaign_id, product_name, dose, applied_at, operator_id, notes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		app.ID, app.TenantID, app.LotID, app.CampaignID, app.ProductName,
		app.Dose, app.AppliedAt, app.OperatorID, app.Notes)
	if err != nil {
		return mapPgErr("insert application", err)
	}
	return nil
}
