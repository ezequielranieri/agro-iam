// Package main integration tests for the seed command prove the SD1
// "reseed stability" scenario: seeding twice (and seeding again after a full
// wipe) leaves the demo dataset byte-stable — same two tenants, same admin
// credentials, same row counts, no duplicates and no unique-violation errors.
//
// They follow the project's integration pattern: skipped unless
// TEST_DATABASE_URL is set, so `go test ./...` stays green without postgres.
// When set, the app schema is dropped and rebuilt from migrations/*.up.sql,
// so the seed always starts from a known-empty state.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		fmt.Println("TEST_DATABASE_URL not set; skipping seed integration tests")
		os.Exit(m.Run())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to test database: %v\n", err)
		os.Exit(1)
	}
	// The schema is a disposable artifact: drop it so a repeated `go test` run
	// is idempotent (otherwise CREATE TABLE collides with the previous run).
	if _, err := setup.Exec(ctx, "DROP SCHEMA IF EXISTS app CASCADE"); err != nil {
		fmt.Fprintf(os.Stderr, "drop app schema: %v\n", err)
		setup.Close()
		os.Exit(1)
	}
	if err := applyMigrations(ctx, setup); err != nil {
		fmt.Fprintf(os.Stderr, "apply migrations: %v\n", err)
		setup.Close()
		os.Exit(1)
	}
	setup.Close()

	if err := os.Setenv("DATABASE_URL", dsn); err != nil {
		fmt.Fprintf(os.Stderr, "set DATABASE_URL: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// applyMigrations executes every migrations/*.up.sql file. `go test` runs with
// cwd = the package directory, so ../../migrations always resolves to the
// repository root. pgx uses the simple protocol for queries without arguments,
// so a whole migration file executes in one call.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(files)
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", file, err)
		}
	}
	return nil
}

// TestSeedReseedStable proves SD1's reseed scenario end to end:
//  1. a fresh seed creates the full demo dataset,
//  2. seeding again is a no-op (idempotent — no unique violations, no dups),
//  3. seeding after a full wipe rebuilds the exact same structure, and the
//     realm (tenant) registry still resolves the same two names.
//
// NOTE: assertion belongs to the seed, not to RLS isolation — the postgres
// package already proves WITH TENANT behavior; here we assert structure.
func TestSeedReseedStable(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping seed integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	if err := run(); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	assertDemoState(t, ctx, pool, "after first seed")

	if err := run(); err != nil {
		t.Fatalf("second seed must be idempotent (no unique violations): %v", err)
	}
	assertDemoState(t, ctx, pool, "after second seed")

	if _, err := pool.Exec(ctx, "TRUNCATE app.tenants CASCADE"); err != nil {
		t.Fatalf("wipe tenants: %v", err)
	}
	if err := run(); err != nil {
		t.Fatalf("seed after full wipe: %v", err)
	}
	assertDemoState(t, ctx, pool, "after wipe and reseed")
}

// assertDemoState checks the seeded database exactly matches demoPlan: tenant
// names in order, per-tenant user/lot/campaign/application counts, the
// byte-stable admin email, and the NULL-operator share of applications.
func assertDemoState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, phase string) {
	t.Helper()

	plan := demoPlan()

	var names []string
	var ids []string
	rows, err := pool.Query(ctx, `SELECT id, name FROM app.tenants ORDER BY name`)
	if err != nil {
		t.Fatalf("[%s] query tenants: %v", phase, err)
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("[%s] scan tenant: %v", phase, err)
		}
		names = append(names, name)
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("[%s] iterate tenants: %v", phase, err)
	}
	if len(names) != 2 {
		t.Fatalf("[%s] expected 2 tenants, got %d", phase, len(names))
	}
	if names[0] != plan[0].name || names[1] != plan[1].name {
		t.Fatalf("[%s] realm registry drifted: got %q, %q; want %q, %q",
			phase, names[0], names[1], plan[0].name, plan[1].name)
	}

	for i, tenant := range plan {
		wantNull := 0
		for _, a := range tenant.applications {
			if a.operatorEmail == "" {
				wantNull++
			}
		}

		err := withTenant(ctx, pool, ids[i], func(tx pgx.Tx) error {
			var users, lots, campaigns, apps, nullOps, admins int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.users`).Scan(&users); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.lots`).Scan(&lots); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.campaigns`).Scan(&campaigns); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.applications`).Scan(&apps); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.applications WHERE operator_id IS NULL`).Scan(&nullOps); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.users WHERE email = $1`, tenant.users[0].email).Scan(&admins); err != nil {
				return err
			}

			if users != len(tenant.users) {
				t.Errorf("[%s] %s: users = %d, want %d", phase, tenant.name, users, len(tenant.users))
			}
			if lots != len(tenant.lots) {
				t.Errorf("[%s] %s: lots = %d, want %d", phase, tenant.name, lots, len(tenant.lots))
			}
			if campaigns != len(tenant.campaigns) {
				t.Errorf("[%s] %s: campaigns = %d, want %d", phase, tenant.name, campaigns, len(tenant.campaigns))
			}
			if apps != len(tenant.applications) {
				t.Errorf("[%s] %s: applications = %d, want %d", phase, tenant.name, apps, len(tenant.applications))
			}
			if nullOps != wantNull {
				t.Errorf("[%s] %s: NULL operators = %d, want %d", phase, tenant.name, nullOps, wantNull)
			}
			if admins != 1 {
				t.Errorf("[%s] %s: admin email %q rows = %d, want 1", phase, tenant.name, tenant.users[0].email, admins)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("[%s] assert %s: %v", phase, tenant.name, err)
		}
	}
}
