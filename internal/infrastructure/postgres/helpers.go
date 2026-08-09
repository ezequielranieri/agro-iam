package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// mapPgErr translates driver errors into domain errors so application code
// never depends on pgx error types. Unique violations (e.g. the global UNIQUE
// on users.email) surface as domain.ErrConflict; RLS rejections (FORCE policy
// denying the row, code 42501 insufficient_privilege) surface as
// domain.ErrForbidden — the tenant is authenticated but not allowed to touch
// that row.
func mapPgErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return domain.ErrConflict
		case "42501": // insufficient_privilege — RLS FORCE rejected the row
			return domain.ErrForbidden
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
