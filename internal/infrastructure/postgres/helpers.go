package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// mapPgErr translates driver errors into domain errors so application code
// never depends on pgx error types. Unique violations (e.g. the global UNIQUE
// on users.email) surface as domain.ErrConflict.
func mapPgErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return domain.ErrConflict
	}
	return fmt.Errorf("%s: %w", op, err)
}
