package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// CampaignRepo is the pgx implementation of ports.CampaignRepository.
type CampaignRepo struct {
	pool *pgxpool.Pool
}

func NewCampaignRepo(pool *pgxpool.Pool) *CampaignRepo {
	return &CampaignRepo{pool: pool}
}

const campaignColumns = `id, tenant_id, name, season, started_at, ended_at, created_at, updated_at`

func scanCampaign(row pgx.Row) (*domain.Campaign, error) {
	var c domain.Campaign
	if err := row.Scan(
		&c.ID, &c.TenantID, &c.Name, &c.Season, &c.StartedAt, &c.EndedAt,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CampaignRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+campaignColumns+` FROM app.campaigns WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	c, err := scanCampaign(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query campaign by id: %w", err)
	}
	return c, nil
}

func (r *CampaignRepo) List(ctx context.Context, tenantID string) ([]*domain.Campaign, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+campaignColumns+` FROM app.campaigns WHERE tenant_id = $1 ORDER BY started_at DESC NULLS LAST`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	defer rows.Close()

	var campaigns []*domain.Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, fmt.Errorf("scan campaign: %w", err)
		}
		campaigns = append(campaigns, c)
	}
	return campaigns, rows.Err()
}

func (r *CampaignRepo) Create(ctx context.Context, campaign *domain.Campaign) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO app.campaigns (id, tenant_id, name, season, started_at, ended_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		campaign.ID, campaign.TenantID, campaign.Name, campaign.Season,
		campaign.StartedAt, campaign.EndedAt)
	if err != nil {
		return mapPgErr("insert campaign", err)
	}
	return nil
}
