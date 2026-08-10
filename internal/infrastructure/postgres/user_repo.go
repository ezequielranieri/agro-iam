package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// UserRepo is the pgx implementation of ports.UserRepository.
type UserRepo struct {
	pool *pgxpool.Pool
}

// NewUserRepo wires a pool into the repository.
func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

const userColumns = `id, tenant_id, email, password_hash, full_name, is_active, created_at, updated_at`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	if err := row.Scan(
		&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.FullName,
		&u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &u, nil
}

// FindByEmail fetches a user. The query runs inside WithTenant: RLS (FORCED on
// app.users) only returns rows whose tenant_id matches app.current_tenant_id(),
// and that GUC is bound per transaction. WITHOUT the tenant context the policy
// predicate evaluates to NULL and no row is ever visible â€” so a login that
// skips WithTenant would fail for every user, even the correct one. The explicit
// tenant_id filter is intentionally absent here: RLS is the enforcement, and the
// tenantID parameter documents intent while the transaction scope is what makes
// it real.
func (r *UserRepo) FindByEmail(ctx context.Context, tenantID, email string) (*domain.User, error) {
	var u *domain.User
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+userColumns+` FROM app.users WHERE email = $1`, email)
		found, err := scanUser(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query user by email: %w", err)
		}
		u = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *UserRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.User, error) {
	var u *domain.User
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		row := tx.QueryRow(ctx,
			`SELECT `+userColumns+` FROM app.users WHERE id = $1`, id)
		found, err := scanUser(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("query user by id: %w", err)
		}
		u = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// List returns every user of the tenant inside the tenant context. RLS hides
// other tenants' rows entirely, so no explicit tenant filter is needed.
func (r *UserRepo) List(ctx context.Context, tenantID string) ([]*domain.User, error) {
	var users []*domain.User
	err := WithTenant(ctx, r.pool, tenantID, func(tx pgxTx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+userColumns+` FROM app.users ORDER BY created_at`)
		if err != nil {
			return fmt.Errorf("list users: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			u, err := scanUser(rows)
			if err != nil {
				return fmt.Errorf("scan user: %w", err)
			}
			users = append(users, u)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserRepo) Create(ctx context.Context, user *domain.User) error {
	// The RLS tenant context comes from user.TenantID: the INSERT can only
	// succeed inside that tenant's policy (WITH CHECK).
	return WithTenant(ctx, r.pool, user.TenantID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO app.users (id, tenant_id, email, password_hash, full_name, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			user.ID, user.TenantID, user.Email, user.PasswordHash, user.FullName, user.IsActive)
		if err != nil {
			return mapPgErr("insert user", err)
		}
		return nil
	})
}

func (r *UserRepo) Update(ctx context.Context, user *domain.User) error {
	return WithTenant(ctx, r.pool, user.TenantID, func(tx pgxTx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE app.users
			 SET email = $2, password_hash = $3, full_name = $4, is_active = $5, updated_at = now()
			 WHERE id = $1`,
			user.ID, user.Email, user.PasswordHash, user.FullName, user.IsActive)
		if err != nil {
			return mapPgErr("update user", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
}
