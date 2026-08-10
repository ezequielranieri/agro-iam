// Package ports defines the interfaces between the application core and the
// outside world. Following the Dependency Inversion Principle, the application
// service depends only on these small interfaces (defined here) while the
// infrastructure implementations satisfy them. Nothing in this package imports
// pgx, redis or any framework.
package ports

import (
	"context"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// UserRepository persists users.
type UserRepository interface {
	// FindByEmail returns the user with the given email in the given tenant,
	// or domain.ErrNotFound.
	FindByEmail(ctx context.Context, tenantID, email string) (*domain.User, error)
	// FindByID returns a user or domain.ErrNotFound.
	FindByID(ctx context.Context, tenantID, id string) (*domain.User, error)
	// Create inserts a new user; returns domain.ErrConflict on duplicate email.
	Create(ctx context.Context, user *domain.User) error
	// Update persists changes to an existing user.
	Update(ctx context.Context, user *domain.User) error
}

// TenantRepository persists tenants. Tenants form the public registry â€” no RLS.
type TenantRepository interface {
	FindByID(ctx context.Context, id string) (*domain.Tenant, error)
	Create(ctx context.Context, tenant *domain.Tenant) error
}

// LotRepository persists lots, always scoped to the tenant session.
type LotRepository interface {
	FindByID(ctx context.Context, tenantID, id string) (*domain.Lot, error)
	List(ctx context.Context, tenantID string) ([]*domain.Lot, error)
	Create(ctx context.Context, lot *domain.Lot) error
}

// CampaignRepository persists campaigns, always scoped to the tenant session.
// Implementations run every query inside WithTenant: RLS is the enforcement
// point, so the SQL carries no explicit tenant_id filter (user_repo pattern).
type CampaignRepository interface {
	FindByID(ctx context.Context, tenantID, id string) (*domain.Campaign, error)
	List(ctx context.Context, tenantID string) ([]*domain.Campaign, error)
	Create(ctx context.Context, campaign *domain.Campaign) error
	// Update persists changes to an existing campaign (full-row replace, no
	// partial PATCH semantics); returns domain.ErrNotFound when the row is
	// missing or belongs to another tenant.
	Update(ctx context.Context, campaign *domain.Campaign) error
	// Delete removes the campaign; returns domain.ErrNotFound when the row is
	// missing or belongs to another tenant.
	Delete(ctx context.Context, tenantID, id string) error
}

// ApplicationRepository persists input applications, always scoped to the
// tenant session. Like CampaignRepository, every query runs inside WithTenant.
type ApplicationRepository interface {
	FindByID(ctx context.Context, tenantID, id string) (*domain.Application, error)
	List(ctx context.Context, tenantID string) ([]*domain.Application, error)
	Create(ctx context.Context, app *domain.Application) error
	// Update persists changes to an existing application (full-row replace);
	// returns domain.ErrNotFound when the row is missing or belongs to another
	// tenant.
	Update(ctx context.Context, app *domain.Application) error
	// Delete removes the application; returns domain.ErrNotFound when the row
	// is missing or belongs to another tenant.
	Delete(ctx context.Context, tenantID, id string) error
}

// AuditRepository appends audit entries. There is intentionally no update or
// delete â€” the audit log is append-only.
type AuditRepository interface {
	Append(ctx context.Context, entry *domain.AuditEntry) error
	// Tail returns the newest chained entry (seq + chain_hash) for the tenant,
	// or nil when the tenant has none. It reads inside a WithTenant transaction
	// with FOR UPDATE to narrow concurrent-appends races.
	Tail(ctx context.Context, tenantID string) (*domain.AuditEntry, error)
	// ListByTenant returns every entry of the tenant ordered by seq ascending,
	// used by chain verification.
	ListByTenant(ctx context.Context, tenantID string) ([]*domain.AuditEntry, error)
}
