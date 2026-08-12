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

// ApplicationInput is the tenant-independent payload of an application write.
// AppliedAt is required at the domain level (schema NOT NULL, DEFAULT now());
// a zero AppliedAt is defaulted by the service clock. OperatorID is nullable:
// the empty string maps to NULL in the repository (nullableUUID precedent).
type ApplicationInput struct {
	LotID       string
	CampaignID  string
	ProductName string
	Dose        string
	AppliedAt   time.Time
	OperatorID  string
	Notes       string
}

// ApplicationService is the use-case boundary for application management.
// Every operation is scoped to a tenant, which the HTTP layer takes from the
// authenticated claims. Create/Update/Delete emit audit events (fail-open)
// with the authenticated actor user id.
type ApplicationService interface {
	// List returns every application owned by the tenant.
	List(ctx context.Context, tenantID string) ([]*domain.Application, error)
	// GetByID returns one application or domain.ErrNotFound.
	GetByID(ctx context.Context, tenantID, id string) (*domain.Application, error)
	// Create validates and persists a new application owned by the tenant.
	// actorUserID is recorded as the audit actor.
	Create(ctx context.Context, tenantID, actorUserID string, in ApplicationInput) (*domain.Application, error)
	// Update replaces the mutable fields of an existing application (full-row
	// replace, no partial PATCH semantics); returns domain.ErrNotFound when
	// the row is missing or belongs to another tenant.
	Update(ctx context.Context, tenantID, actorUserID, id string, in ApplicationInput) (*domain.Application, error)
	// Delete removes the application; returns domain.ErrNotFound when the row
	// is missing or belongs to another tenant.
	Delete(ctx context.Context, tenantID, actorUserID, id string) error
}

// UserInput is the tenant-independent payload of a user provisioning create
// (R9). Password is the plaintext to hash with Argon2id; it never reaches the
// repository or any response.
type UserInput struct {
	Email    string
	Password string
	FullName string
	Role     string
}

// UpdateUserInput is the payload of a user update: a full-row replace of the
// mutable fields (no partial PATCH semantics — the design decision resolved in
// the slice 4 tasks).
type UpdateUserInput struct {
	FullName string
	IsActive bool
}

// UserService is the use-case boundary for user provisioning. Every operation
// is scoped to a tenant, which the HTTP layer takes from the authenticated
// claims. Responses never carry password material (R9).
type UserService interface {
	// CreateUser validates the role code, hashes the password (Argon2id PHC),
	// persists the user and assigns the initial role. actorUserID is recorded
	// as the audit actor. Returns domain.ErrConflict when the email already
	// exists — in the same tenant or any other (global unique index, R11).
	CreateUser(ctx context.Context, tenantID, actorUserID string, in UserInput) (*domain.User, error)
	// ListUsers returns every user of the tenant.
	ListUsers(ctx context.Context, tenantID string) ([]*domain.User, error)
	// UpdateUser replaces the mutable fields of an existing user (full_name,
	// is_active toggle); returns domain.ErrNotFound when the row is missing or
	// belongs to another tenant.
	UpdateUser(ctx context.Context, tenantID, actorUserID, id string, in UpdateUserInput) (*domain.User, error)
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
	// Latest returns the tenant's most recent `limit` entries, newest first
	// (seq DESC). It is the read side of the admin audit screen (AP1): the
	// repository runs the query inside WithTenant, so RLS — not a WHERE
	// clause — guarantees a tenant can never read another's trail.
	Latest(ctx context.Context, tenantID string, limit int) ([]*domain.AuditEntry, error)
}
