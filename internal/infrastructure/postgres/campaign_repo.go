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

// CampaignRepo is the pgx implementation of ports.CampaignRepository.
//
// Every query runs inside WithTenant, which binds app.tenant_id for the
// lifetime of one transaction, so Postgres Row-Level Security — not a WHERE
// clause — is what isolates tenants (the user_repo pattern). The explicit
// tenant_id filter is intentionally absent: RLS is the sole enforcement point,
// and there are no raw pool queries. Without the tenant context every policy
// predicate is NULL and no row is ever visible.
type CampaignRepo struct {
	pool *pgxpool.Pool
}

func NewCampaignRepo(pool *pgxpool.Pool) *CampaignRepo {
	return &CampaignRepo{pool: pool}
}

// Compile-time contract: CampaignRepo satisfies ports.CampaignRepository.
var _ ports.CampaignRepository = (*CampaignRepo)(nil)

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
	var campaign *domain.Campaign
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+campaignColumns+` FROM app.campaigns WHERE id = $1`, id)
		found, err := scanCampaign(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query campaign by id: %w", err)
		}
		campaign = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return campaign, nil
}

func (r *CampaignRepo) List(ctx context.Context, tenantID string) ([]*domain.Campaign, error) {
	var campaigns []*domain.Campaign
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+campaignColumns+` FROM app.campaigns ORDER BY started_at DESC NULLS LAST`)
		if err != nil {
			return fmt.Errorf("list campaigns: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			c, err := scanCampaign(rows)
			if err != nil {
				return fmt.Errorf("scan campaign: %w", err)
			}
			campaigns = append(campaigns, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return campaigns, nil
}

func (r *CampaignRepo) Create(ctx context.Context, campaign *domain.Campaign) error {
	// The RLS tenant context is bound from campaign.TenantID: the INSERT can
	// only succeed inside that tenant's policy (WITH CHECK).
	return WithTenant(ctx, r.pool, campaign.TenantID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO app.campaigns (id, tenant_id, name, season, started_at, ended_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			campaign.ID, campaign.TenantID, campaign.Name, campaign.Season,
			campaign.StartedAt, campaign.EndedAt)
		if err != nil {
			return mapPgErr("insert campaign", err)
		}
		return nil
	})
}

// Update is a full-row replace (UserRepo.Update style): every mutable column is
// rewritten, no partial PATCH semantics. RowsAffected()==0 means the row does
// not exist or is hidden by RLS (owned by another tenant), surfaced as
// domain.ErrNotFound.
func (r *CampaignRepo) Update(ctx context.Context, campaign *domain.Campaign) error {
	return WithTenant(ctx, r.pool, campaign.TenantID, func(tx pgxTx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE app.campaigns
			 SET name = $2, season = $3, started_at = $4, ended_at = $5, updated_at = now()
			 WHERE id = $1`,
			campaign.ID, campaign.Name, campaign.Season, campaign.StartedAt, campaign.EndedAt)
		if err != nil {
			return mapPgErr("update campaign", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}

func (r *CampaignRepo) Delete(ctx context.Context, tenantID, id string) error {
	return WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM app.campaigns WHERE id = $1`, id)
		if err != nil {
			return mapPgErr("delete campaign", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}
