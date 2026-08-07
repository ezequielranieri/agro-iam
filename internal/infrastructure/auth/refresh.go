package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// RefreshTokenStore is the pgx implementation of ports.RefreshTokenRepository.
// Tokens are stored hashed (SHA-256) so a database leak does not expose usable
// credentials. Family bookkeeping (family_id, replaced_by) drives rotation and
// replay detection in the application service.
type RefreshTokenStore struct {
	pool *pgxpool.Pool
}

func NewRefreshTokenStore(pool *pgxpool.Pool) *RefreshTokenStore {
	return &RefreshTokenStore{pool: pool}
}

func (s *RefreshTokenStore) Store(ctx context.Context, token *ports.RefreshTokenRecord) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO app.refresh_tokens (id, user_id, tenant_id, family_id, token_hash, expires_at)
		 VALUES ($1, $2, $3, $4, $5, to_timestamp($6))`,
		token.ID, token.UserID, token.TenantID, token.FamilyID, token.TokenHash, token.ExpiresAt)
	if err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}
	return nil
}

func (s *RefreshTokenStore) FindByHash(ctx context.Context, hash string) (*ports.RefreshTokenRecord, error) {
	var rec ports.RefreshTokenRecord
	var expiresAt time.Time
	var revokedAt *time.Time
	// replaced_by is NULL until the token is rotated. Scanning into a *string
	// lets pgx represent NULL as nil, which we then normalize to "" â€” the port
	// type is a plain string, so the application layer never sees pgx NULLs.
	var replacedBy *string
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, tenant_id, family_id, token_hash, expires_at, revoked_at, replaced_by
		 FROM app.refresh_tokens WHERE token_hash = $1`, hash,
	).Scan(&rec.ID, &rec.UserID, &rec.TenantID, &rec.FamilyID, &rec.TokenHash,
		&expiresAt, &revokedAt, &replacedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find refresh token: %w", err)
	}
	rec.ExpiresAt = expiresAt.Unix()
	if revokedAt != nil {
		rv := revokedAt.Unix()
		rec.RevokedAt = &rv
	}
	if replacedBy != nil {
		rec.ReplacedBy = *replacedBy
	}
	return &rec, nil
}

func (s *RefreshTokenStore) Revoke(ctx context.Context, id, replacedBy string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE app.refresh_tokens
		 SET revoked_at = now(), replaced_by = $2
		 WHERE id = $1`, id, replacedBy)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

func (s *RefreshTokenStore) RevokeFamily(ctx context.Context, familyID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE app.refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID)
	if err != nil {
		return fmt.Errorf("revoke refresh family: %w", err)
	}
	return nil
}

var _ ports.RefreshTokenRepository = (*RefreshTokenStore)(nil)
