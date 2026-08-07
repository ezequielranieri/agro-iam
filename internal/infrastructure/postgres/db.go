// Package postgres contains the pgx-based persistence implementations.
//
// Tenant-scoped repositories (users, lots, campaigns, ...) run every query
// inside WithTenant, which binds app.tenant_id for the lifetime of one
// transaction. Postgres Row-Level Security — not a WHERE clause — is therefore
// the enforcement point for tenant isolation.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxTx aliases pgx.Tx so repositories can accept "something that runs queries"
// without importing concrete pool types in their signatures.
type pgxTx = pgx.Tx

// NewPool creates a pgx connection pool. Callers must Close() it on shutdown.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// WithTenant runs fn inside a dedicated transaction whose session has the
// app.tenant_id setting bound to tenantID.
//
// WHY set_config(..., true): the third argument (true) scopes the setting to the
// current transaction only. The alternative — a plain SET on the pooled
// connection — would poison the connection for every subsequent query that the
// pool reuses, leaking tenant A's identity into tenant B's queries. RLS reads
// this GUC via app.current_tenant_id(), so a transaction is the only correct
// and safe boundary.
//
// fn receives the transaction; repositories run their queries against it so a
// whole unit of work observes a single tenant context and commits atomically.
func WithTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(tx pgxTx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// local => scoped to this transaction only (see comment above).
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
