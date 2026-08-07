package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// authService implements ports.AuthService by composing the low-level ports.
// It has no idea whether the token manager is JWT, where hashing happens, or
// how refresh tokens are stored â€” that is the whole point of the ports.
type authService struct {
	users         ports.UserRepository
	tenants       ports.TenantRepository
	tokens        ports.TokenManager
	hasher        ports.PasswordHasher
	refreshTokens ports.RefreshTokenRepository
	accessTTL     time.Duration
	refreshTTL    time.Duration
	now           func() time.Time
}

// NewAuthService wires the concrete ports into an AuthService. timeSource is
// injectable for deterministic tests; pass time.Now when unsure.
func NewAuthService(
	users ports.UserRepository,
	tenants ports.TenantRepository,
	tokens ports.TokenManager,
	hasher ports.PasswordHasher,
	refreshTokens ports.RefreshTokenRepository,
	accessTTL time.Duration,
	refreshTTL time.Duration,
) ports.AuthService {
	return &authService{
		users:         users,
		tenants:       tenants,
		tokens:        tokens,
		hasher:        hasher,
		refreshTokens: refreshTokens,
		accessTTL:     accessTTL,
		refreshTTL:    refreshTTL,
		now:           time.Now,
	}
}

// Login validates credentials against the tenant's user repository, then issues
// a fresh token pair. The refresh token belongs to a brand new family.
func (s *authService) Login(ctx context.Context, tenantID, email, password string) (*ports.AuthSession, error) {
	if tenantID == "" || email == "" || password == "" {
		return nil, domain.ErrUnauthorized
	}

	user, err := s.users.FindByEmail(ctx, tenantID, email)
	if err != nil {
		// Do not reveal whether the tenant, email or password was the failure.
		// ErrNotFound and ErrUnauthorized collapse into one response.
		return nil, domain.ErrUnauthorized
	}
	if !user.IsActive {
		return nil, domain.ErrUnauthorized
	}

	ok, err := s.hasher.Verify(user.PasswordHash, password)
	if err != nil || !ok {
		return nil, domain.ErrUnauthorized
	}

	claims := ports.TokenClaims{UserID: user.ID, TenantID: user.TenantID, Role: ""}
	access, err := s.tokens.Issue(claims)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}

	rawRefresh, err := NewRefreshToken(s.refreshTTL)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}
	if err := s.refreshTokens.Store(ctx, &ports.RefreshTokenRecord{
		ID:        rawRefresh.ID,
		UserID:    user.ID,
		TenantID:  user.TenantID,
		FamilyID:  rawRefresh.FamilyID,
		TokenHash: rawRefresh.Hash,
		ExpiresAt: rawRefresh.ExpiresAt.Unix(),
	}); err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}

	return &ports.AuthSession{
		AccessToken:  access,
		RefreshToken: rawRefresh.Plain,
		ExpiresIn:    int64(s.accessTTL.Seconds()),
		UserID:       user.ID,
		TenantID:     user.TenantID,
	}, nil
}

// Refresh implements rotation. The presented token must exist, be unrevoked and
// unexpired. On success the old token is revoked and a new one joins the same
// family. Presenting a token that was already rotated (revoked with a
// replaced_by) is treated as theft: the whole family dies.
func (s *authService) Refresh(ctx context.Context, refreshToken string) (*ports.AuthSession, error) {
	if refreshToken == "" {
		return nil, domain.ErrUnauthorized
	}

	hash := HashToken(refreshToken)
	record, err := s.refreshTokens.FindByHash(ctx, hash)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, domain.ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("find refresh token: %w", err)
	}

	now := s.now()

	// The decisive checks, factored into a pure function so the rotation and
	// replay rules can be unit-tested with no database.
	decision := DecideRotation(record.RevokedAt != nil, record.ReplacedBy, record.ExpiresAt, now.Unix())

	switch decision {
	case RotationRejectRevoked:
		// A revoked token with a replacement is the classic replay attack
		// signature â€” kill the whole family.
		if err := s.refreshTokens.RevokeFamily(ctx, record.FamilyID); err != nil {
			return nil, fmt.Errorf("revoke family: %w", err)
		}
		return nil, domain.ErrUnauthorized

	case RotationRejectExpired:
		return nil, domain.ErrUnauthorized

	case RotationAllow:
		// Generate the successor inside the same family.
		next, err := NewRefreshToken(s.refreshTTL)
		if err != nil {
			return nil, fmt.Errorf("generate refresh token: %w", err)
		}
		next.FamilyID = record.FamilyID

		// Mark the presented token as revoked and note who replaced it.
		if err := s.refreshTokens.Revoke(ctx, record.ID, next.ID); err != nil {
			return nil, fmt.Errorf("revoke old refresh token: %w", err)
		}
		if err := s.refreshTokens.Store(ctx, &ports.RefreshTokenRecord{
			ID:         next.ID,
			UserID:     record.UserID,
			TenantID:   record.TenantID,
			FamilyID:   record.FamilyID,
			TokenHash:  next.Hash,
			ExpiresAt:  next.ExpiresAt.Unix(),
			ReplacedBy: "",
		}); err != nil {
			return nil, fmt.Errorf("store rotated refresh token: %w", err)
		}

		access, err := s.tokens.Issue(ports.TokenClaims{
			UserID:   record.UserID,
			TenantID: record.TenantID,
		})
		if err != nil {
			return nil, fmt.Errorf("issue access token: %w", err)
		}

		return &ports.AuthSession{
			AccessToken:  access,
			RefreshToken: next.Plain,
			ExpiresIn:    int64(s.accessTTL.Seconds()),
			UserID:       record.UserID,
			TenantID:     record.TenantID,
		}, nil

	default:
		return nil, domain.ErrUnauthorized
	}
}

var _ ports.AuthService = (*authService)(nil)
