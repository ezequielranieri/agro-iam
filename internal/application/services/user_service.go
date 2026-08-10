package services

import (
	"context"
	"fmt"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// userService implements ports.UserService. It owns the provisioning business
// rules (role-code validation, Argon2id hashing via the hasher port, identity)
// and delegates persistence to the user and role-membership repositories. RLS
// enforcement is the repositories' concern, not the service's. CreateUser is
// intentionally non-atomic (user row, then role membership — two WithTenant
// transactions, matching the seed pattern): validating the role FIRST means an
// invalid code never creates a user row.
type userService struct {
	users     ports.UserRepository
	userRoles ports.UserRoleRepository
	hasher    ports.PasswordHasher
	signals   ports.BreachSignalSink
	now       func() time.Time
}

// NewUserService wires the repositories and the password hasher into a
// UserService. signals may be nil (emission is a no-op). The clock is
// injectable for deterministic tests; pass time.Now when unsure.
func NewUserService(users ports.UserRepository, userRoles ports.UserRoleRepository, hasher ports.PasswordHasher, signals ports.BreachSignalSink) ports.UserService {
	return &userService{users: users, userRoles: userRoles, hasher: hasher, signals: signals, now: time.Now}
}

// CreateUser validates the role code FIRST (an unknown code must never reach
// SQL), hashes the plaintext with Argon2id (the hash, never the plaintext, is
// persisted), inserts the user, then assigns the initial role. A duplicate
// email — same tenant or another — surfaces as domain.ErrConflict via the
// global unique index on users.email (R11). The authenticated actor is
// recorded as the audit actor of the user.create event.
func (s *userService) CreateUser(ctx context.Context, tenantID, actorUserID string, in ports.UserInput) (*domain.User, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}
	if !domain.IsValidRoleCode(in.Role) {
		return nil, domain.ErrInvalidInput
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	now := s.now()
	user := &domain.User{
		ID:           newUUIDish(),
		TenantID:     tenantID,
		Email:        in.Email,
		PasswordHash: hash,
		FullName:     in.FullName,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if !user.IsValid() {
		return nil, domain.ErrInvalidInput
	}

	if err := s.users.Create(ctx, user); err != nil {
		return nil, err
	}
	if err := s.userRoles.Assign(ctx, &domain.UserRole{
		UserID:   user.ID,
		RoleCode: in.Role,
		TenantID: tenantID,
	}); err != nil {
		return nil, err
	}

	emitEvent(ctx, s.signals, tenantID, actorUserID, "", false,
		&Event{Action: "user.create", Severity: domain.SeverityInfo, EmitAudit: true}, "")
	return user, nil
}

// ListUsers returns every user of the tenant. The tenant id is the scoping key
// the repository threads into the RLS transaction.
func (s *userService) ListUsers(ctx context.Context, tenantID string) ([]*domain.User, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}
	return s.users.List(ctx, tenantID)
}

// UpdateUser replaces the mutable fields of an existing user (full_name,
// is_active toggle). It is a full-row replace (UserRepo.Update style): the
// current row is fetched so email and hash are preserved, then the mutable
// fields are rewritten. A missing or other-tenant row (hidden by RLS) surfaces
// as domain.ErrNotFound.
func (s *userService) UpdateUser(ctx context.Context, tenantID, actorUserID, id string, in ports.UpdateUserInput) (*domain.User, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantRequired
	}

	user, err := s.users.FindByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	user.FullName = in.FullName
	user.IsActive = in.IsActive
	user.UpdatedAt = s.now()
	if !user.IsValid() {
		return nil, domain.ErrInvalidInput
	}
	if err := s.users.Update(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

var _ ports.UserService = (*userService)(nil)
