package ports

import (
	"context"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// TokenClaims is the payload carried inside an access token.
type TokenClaims struct {
	UserID   string
	TenantID string
	Role     string
}

// TokenManager issues and verifies short-lived access tokens (JWT, HS256).
type TokenManager interface {
	// Issue creates a new access token for the given claims.
	Issue(claims TokenClaims) (string, error)
	// Verify parses and validates a token, returning its claims or an error.
	Verify(token string) (*TokenClaims, error)
}

// PasswordHasher hashes and verifies passwords. The implementation (Argon2id)
// is an infrastructure concern; the application only needs this contract.
type PasswordHasher interface {
	// Hash encodes a password into a self-contained PHC string.
	Hash(password string) (string, error)
	// Verify checks a password against a previously encoded hash string.
	Verify(encoded, password string) (bool, error)
}

// RefreshTokenRepository persists opaque refresh tokens (hashed) and their
// families so rotation and replay detection can be enforced.
type RefreshTokenRepository interface {
	// Store writes a refresh token record.
	Store(ctx context.Context, token *RefreshTokenRecord) error
	// FindByHash returns the token record by SHA-256 hash, or domain.ErrNotFound.
	FindByHash(ctx context.Context, hash string) (*RefreshTokenRecord, error)
	// Revoke marks a token revoked, optionally noting the token that replaced it.
	Revoke(ctx context.Context, id, replacedBy string) error
	// RevokeFamily revokes every still-active token in a family (replay response).
	RevokeFamily(ctx context.Context, familyID string) error
}

// RefreshTokenRecord mirrors the app.refresh_tokens row at the port level so the
// service does not need to import the domain schema for this infrastructure-
// owned concept.
type RefreshTokenRecord struct {
	ID         string
	UserID     string
	TenantID   string
	FamilyID   string
	TokenHash  string
	ExpiresAt  int64 // unix seconds
	RevokedAt  *int64
	ReplacedBy string
}

// AuthService is the use-case boundary for authentication. It is consumed by the
// HTTP layer, which knows nothing about JWTs or Argon2id.
type AuthService interface {
	// Login validates credentials and returns an access token, an opaque
	// refresh token and the access token lifetime in seconds.
	Login(ctx context.Context, tenantID, email, password string) (*AuthSession, error)
	// Refresh rotates a refresh token: the presented token is revoked and a new
	// one is issued in the same family. A previously-used token triggers full
	// family revocation (replay detection).
	Refresh(ctx context.Context, refreshToken string) (*AuthSession, error)
}

// AuthSession is the result of a successful Login or Refresh.
type AuthSession struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64 // seconds until the access token expires
	UserID       string
	TenantID     string
	Role         string
}

// LotService is the use-case boundary for lot management. Every operation is
// scoped to a tenant, which the HTTP layer takes from the authenticated claims.
type LotService interface {
	// ListByTenant returns every lot owned by the tenant.
	ListByTenant(ctx context.Context, tenantID string) ([]*domain.Lot, error)
	// Create validates and persists a new lot owned by the tenant. actorUserID
	// is the authenticated user performing the action (from the JWT claims),
	// recorded as the audit actor.
	Create(ctx context.Context, tenantID, actorUserID, name string, areaHA float64, crop string) (*domain.Lot, error)
}

// CampaignInput is the tenant-independent payload of a campaign write. The
// date fields are optional pointers (RefreshTokenRecord precedent): a nil date
// simply is not set, which is distinct from a zero time.
type CampaignInput struct {
	Name      string
	Season    string
	StartedAt *time.Time
	EndedAt   *time.Time
}

// CampaignService is the use-case boundary for campaign management. Every
// operation is scoped to a tenant, which the HTTP layer takes from the
// authenticated claims. Create/Update/Delete emit audit events (fail-open)
// with the authenticated actor user id.
type CampaignService interface {
	// List returns every campaign owned by the tenant.
	List(ctx context.Context, tenantID string) ([]*domain.Campaign, error)
	// GetByID returns one campaign or domain.ErrNotFound.
	GetByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error)
	// Create validates and persists a new campaign owned by the tenant.
	// actorUserID is recorded as the audit actor.
	Create(ctx context.Context, tenantID, actorUserID string, in CampaignInput) (*domain.Campaign, error)
	// Update replaces the mutable fields of an existing campaign (full-row
	// replace, no partial PATCH semantics); returns domain.ErrNotFound when
	// the row is missing or belongs to another tenant.
	Update(ctx context.Context, tenantID, actorUserID, id string, in CampaignInput) (*domain.Campaign, error)
	// Delete removes the campaign; returns domain.ErrNotFound when the row is
	// missing or belongs to another tenant.
	Delete(ctx context.Context, tenantID, actorUserID, id string) error
}

// AuditService is the use-case boundary for the tamper-evident audit trail.
// Emitters (services and handlers) call Record after security-relevant events;
// failures are WARN-logged and never fail the caller (fail-open).
type AuditService interface {
	// Record appends a chained entry for the tenant: it reads the tail, links
	// the new entry to it (seq + prev_hash) and inserts. On a UNIQUE conflict
	// (concurrent append) it retries once; on persistent failure it WARN-logs
	// and returns the error — the caller proceeds regardless.
	Record(ctx context.Context, tenantID, actorUserID, action, entityType, entityID string,
		payload []byte, severity string) error
	// VerifyChain recomputes the tenant's chain from all stored rows and
	// returns the first broken seq (0 = intact). Internal only — there is no
	// public endpoint.
	VerifyChain(ctx context.Context, tenantID string) (int64, error)
}
