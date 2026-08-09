package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// TestMapPgErr_UniqueViolation maps 23505 to domain.ErrConflict.
func TestMapPgErr_UniqueViolation(t *testing.T) {
	err := &pgconn.PgError{Code: "23505"}
	if got := mapPgErr("insert", err); !errors.Is(got, domain.ErrConflict) {
		t.Fatalf("mapPgErr(23505) = %v, want domain.ErrConflict", got)
	}
}

// TestMapPgErr_RLSRejection maps 42501 (RLS FORCE denied the row) to
// domain.ErrForbidden — the tenant is authenticated but not allowed to touch
// the row.
func TestMapPgErr_RLSRejection(t *testing.T) {
	err := &pgconn.PgError{Code: "42501"}
	if got := mapPgErr("select", err); !errors.Is(got, domain.ErrForbidden) {
		t.Fatalf("mapPgErr(42501) = %v, want domain.ErrForbidden", got)
	}
}

// TestMapPgErr_UnknownCode wraps with the operation, not a domain error.
func TestMapPgErr_UnknownCode(t *testing.T) {
	err := &pgconn.PgError{Code: "22001"} // string_data_right_truncation
	got := mapPgErr("insert", err)
	if errors.Is(got, domain.ErrConflict) || errors.Is(got, domain.ErrForbidden) {
		t.Fatalf("mapPgErr(22001) = %v, want a wrapped generic error", got)
	}
}
