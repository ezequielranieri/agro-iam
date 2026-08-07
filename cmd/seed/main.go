// Command seed inserts demo data for local development: two tenants with an
// admin user each and a couple of lots per tenant. Passwords are hashed with
// the project's real Argon2id hasher, so login against the API works as-is.
//
// Usage: DATABASE_URL=postgres://agroiam:agroiam@localhost:5432/agroiam?sslmode=disable go run ./cmd/seed
//
// This is a dev-only tool; it is intentionally not part of the API server.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/infrastructure/auth"
)

// demoTenant is one tenant seeded for the isolation demo.
type demoTenant struct {
	name    string
	email   string
	pass    string
	roles   []string
	lots    []string
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	hasher := auth.NewPasswordHasher()

	tenants := []demoTenant{
		{
			name:  "Coop La Esperanza",
			email: "admin@esperanza.coop",
			pass:  "test123",
			roles: []string{"admin"},
			lots:  []string{"Lote Norte - Soja", "Lote Sur - Maiz"},
		},
		{
			name:  "Estancia El Algarrobo",
			email: "admin@algarrobo.campo",
			pass:  "test123",
			roles: []string{"admin"},
			lots:  []string{"Lote Este - Trigo", "Lote Oeste - Cebada"},
		},
	}

	for _, t := range tenants {
		tenantID, err := createTenant(ctx, pool, t.name)
		if err != nil {
			return fmt.Errorf("tenant %q: %w", t.name, err)
		}

		hash, err := hasher.Hash(t.pass)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}

		userID, err := createUser(ctx, pool, tenantID, t.email, hash, "Admin " + t.name)
		if err != nil {
			return fmt.Errorf("user %q: %w", t.email, err)
		}

		for _, role := range t.roles {
			if err := assignRole(ctx, pool, tenantID, userID, role); err != nil {
				return fmt.Errorf("role %q: %w", role, err)
			}
		}

		for _, name := range t.lots {
			if err := createLot(ctx, pool, tenantID, userID, name); err != nil {
				return fmt.Errorf("lot %q: %w", name, err)
			}
		}

		fmt.Printf("tenant=%s id=%s user=%s password=%s\n", t.name, tenantID, t.email, t.pass)
	}
	fmt.Println("seed done")
	return nil
}

// The tenant registry is not RLS-protected, so it is created with a plain
// INSERT on the pool.
func createTenant(ctx context.Context, pool *pgxpool.Pool, name string) (string, error) {
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO app.tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	return id, err
}

// createUser runs inside WithTenant so the FORCED RLS on app.users accepts the
// INSERT (the GUC must be bound to this tenant). We replicate the project's
// tenant-scoped transaction pattern here because cmd/seed is a dev tool, not an
// API endpoint.
func createUser(ctx context.Context, pool *pgxpool.Pool, tenantID, email, hash, fullName string) (string, error) {
	var id string
	err := withTenant(ctx, pool, tenantID, func(t pgx.Tx) error {
		return t.QueryRow(ctx,
			`INSERT INTO app.users (tenant_id, email, password_hash, full_name)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			tenantID, email, hash, fullName).Scan(&id)
	})
	return id, err
}

func assignRole(ctx context.Context, pool *pgxpool.Pool, tenantID, userID, role string) error {
	return withTenant(ctx, pool, tenantID, func(t pgx.Tx) error {
		_, err := t.Exec(ctx,
			`INSERT INTO app.user_roles (user_id, role_code, tenant_id) VALUES ($1, $2, $3)`,
			userID, role, tenantID)
		return err
	})
}

func createLot(ctx context.Context, pool *pgxpool.Pool, tenantID, userID, name string) error {
	return withTenant(ctx, pool, tenantID, func(t pgx.Tx) error {
		_, err := t.Exec(ctx,
			`INSERT INTO app.lots (tenant_id, name, area_ha, crop) VALUES ($1, $2, $3, $4)`,
			tenantID, name, 120.5, name)
		return err
	})
}

// --- tiny tenant-scoped transaction (same pattern as internal/infrastructure/postgres) ---

func withTenant(ctx context.Context, pool *pgxpool.Pool, tenantID string, fn func(pgx.Tx) error) error {
	t, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = t.Rollback(ctx) }()

	// local => scoped to this transaction only; never plain SET on a pooled conn.
	if _, err := t.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return err
	}

	if err := fn(t); err != nil {
		return err
	}
	return t.Commit(ctx)
}
