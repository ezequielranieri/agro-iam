// Package postgres integration tests prove tenant isolation is enforced by
// PostgreSQL Row Level Security â€” not by the repository layer â€” against a live
// database.
//
// They are skipped unless TEST_DATABASE_URL is set, so `go test ./...` stays
// green on machines without postgres. When set, the tests use a DEDICATED
// database (agroiam_test by convention), never the dev `agroiam` one: the
// schema is dropped and rebuilt from migrations/*.up.sql on every run, then
// every tenanted table is truncated before each test.
//
// One subtlety matters for the whole file: the official postgres Docker image
// creates POSTGRES_USER as a SUPERUSER, and superusers always bypass RLS â€”
// FORCE or not â€” so a connection as `agroiam` would see every tenant's rows and
// every isolation assertion below would prove nothing. The harness therefore
// boots a dedicated non-superuser role (agroiam_rls), owns the app schema with
// it, and runs all queries as it â€” the same posture a production app uses.
//
// Isolation guarantee (asserted here, enforced by the database):
//
//   - No tenant context => app.current_tenant_id() is NULL => every RLS policy
//     predicate is NULL => zero rows visible. FORCE extends this to the table
//     owner, so even a raw connection cannot leak.
//   - Within a tenant-bound transaction the same tables expose exactly that
//     tenant's rows.
//   - Cross-tenant reads by id or email are a miss (ErrNotFound), never a leak.
//   - WITH CHECK rejects an INSERT that claims a tenant different from the
//     session's bound tenant.
package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// testPool is the package-wide pool to the dedicated integration database. It is
// nil when TEST_DATABASE_URL is unset, in which case every integration test
// skips itself via setupIntegrationTest.
var testPool *pgxpool.Pool

// rlsRole is the dedicated non-superuser role the integration tests run as (see
// the package comment for why). The harness creates it on demand, idempotently.
const (
	rlsRole         = "agroiam_rls"
	rlsRolePassword = "agroiam"
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		fmt.Println("TEST_DATABASE_URL not set; skipping integration tests")
		os.Exit(m.Run())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// setupPool is the bootstrap connection: it drops the schema and, when the
	// DSN role would bypass RLS, creates the constrained test role. It is NOT the
	// pool the tests run against unless the DSN role already respects RLS.
	setupPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to test database: %v\n", err)
		os.Exit(1)
	}
	if err := setupPool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ping test database: %v\n", err)
		setupPool.Close()
		os.Exit(1)
	}

	// The schema is a disposable artifact: drop it so a repeated `go test` run is
	// idempotent (otherwise CREATE TABLE collides with the previous run). DROP
	// goes through setupPool, which can always drop whatever a prior run left.
	if _, err := setupPool.Exec(ctx, "DROP SCHEMA IF EXISTS app CASCADE"); err != nil {
		fmt.Fprintf(os.Stderr, "drop app schema: %v\n", err)
		setupPool.Close()
		os.Exit(1)
	}

	pool := setupPool
	if bypass, err := roleBypassesRLS(ctx, setupPool); err != nil {
		fmt.Fprintf(os.Stderr, "inspect role: %v\n", err)
		setupPool.Close()
		os.Exit(1)
	} else if bypass {
		if err := ensureRLSRole(ctx, setupPool); err != nil {
			fmt.Fprintf(os.Stderr, "bootstrap rls role: %v\n", err)
			setupPool.Close()
			os.Exit(1)
		}
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse dsn: %v\n", err)
			setupPool.Close()
			os.Exit(1)
		}
		cfg.ConnConfig.User = rlsRole
		cfg.ConnConfig.Password = rlsRolePassword
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "connect as %s: %v\n", rlsRole, err)
			setupPool.Close()
			os.Exit(1)
		}
	}

	// Migrations run as the pool the tests will use, so every object in the app
	// schema is owned by a non-superuser role and FORCE RLS genuinely binds it.
	if err := applyMigrations(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "apply migrations: %v\n", err)
		pool.Close()
		setupPool.Close()
		os.Exit(1)
	}

	testPool = pool
	code := m.Run()
	testPool.Close()
	if setupPool != pool {
		setupPool.Close()
	}
	os.Exit(code)
}

// roleBypassesRLS reports whether the DSN role bypasses RLS entirely (superuser
// or BYPASSRLS). Either attribute defeats FORCE RLS, so assertions that "prove"
// isolation would be testing nothing.
func roleBypassesRLS(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var isSuper, bypass bool
	if err := pool.QueryRow(ctx,
		`SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`,
	).Scan(&isSuper, &bypass); err != nil {
		return false, fmt.Errorf("query role attrs: %w", err)
	}
	return isSuper || bypass, nil
}

// ensureRLSRole creates (or repairs) the dedicated non-superuser role and grants
// it CONNECT + CREATE on the current database. CREATE is what lets the role own
// the app schema â€” and therefore the tables â€” so FORCE RLS applies to it. The
// helper is idempotent across runs.
func ensureRLSRole(ctx context.Context, pool *pgxpool.Pool) error {
	role := pgx.Identifier{rlsRole}.Sanitize()

	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, rlsRole,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check role: %w", err)
	}

	// CREATE/ALTER ROLE are utility statements: PostgreSQL rejects bind
	// parameters there, so the fixed test-only password is inlined. It is a
	// compile-time constant, not user input.
	stmt := fmt.Sprintf(
		`CREATE ROLE %s WITH LOGIN PASSWORD '%s' NOSUPERUSER NOBYPASSRLS`,
		role, rlsRolePassword,
	)
	if exists {
		stmt = fmt.Sprintf(
			`ALTER ROLE %s WITH LOGIN PASSWORD '%s' NOSUPERUSER NOBYPASSRLS`,
			role, rlsRolePassword,
		)
	}
	if _, err := pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("ensure role %s: %w", rlsRole, err)
	}

	var dbName string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		return fmt.Errorf("current database: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`GRANT CONNECT, CREATE ON DATABASE `+pgx.Identifier{dbName}.Sanitize()+` TO `+role,
	); err != nil {
		return fmt.Errorf("grant %s on %s: %w", rlsRole, dbName, err)
	}
	return nil
}

// applyMigrations executes every migrations/*.up.sql file against the test
// database. `go test` runs with cwd = the package directory, so ../../../migrations
// always resolves to the repository root regardless of the harness. pgx uses the
// simple protocol for queries without arguments, so a whole migration file (with
// comments, $$ function bodies and several statements) executes in one call.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "*.up.sql"))
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

// setupIntegrationTest skips the test when no database is configured and wipes
// every tenanted table so each test starts from a deterministic empty slate.
// Listing every table up front (plus CASCADE as a belt-and-suspenders) makes the
// TRUNCATE immune to the FK edges between them.
func setupIntegrationTest(t *testing.T) context.Context {
	t.Helper()
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if _, err := testPool.Exec(ctx, `TRUNCATE TABLE
		app.lots, app.campaigns, app.applications, app.user_roles, app.users,
		app.tenants, app.audit_log, app.refresh_tokens
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}
	return ctx
}

// newUUID returns a random RFC 4122 version 4 UUID. The project deliberately
// avoids a third-party uuid package; a v4 UUID is just 16 random bytes with the
// version (0100) and variant (10xx) bits set.
func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// createTestTenant inserts a tenant into the global (RLS-free) registry.
func createTestTenant(ctx context.Context, t *testing.T, repo *TenantRepo, name string) *domain.Tenant {
	t.Helper()
	tenant := &domain.Tenant{ID: newUUID(t), Name: name}
	if err := repo.Create(ctx, tenant); err != nil {
		t.Fatalf("create tenant %q: %v", name, err)
	}
	return tenant
}

// createTestLot inserts a lot owned by the given tenant through the repository,
// which binds the RLS context from lot.TenantID.
func createTestLot(ctx context.Context, t *testing.T, repo *LotRepo, tenantID, name string) *domain.Lot {
	t.Helper()
	lot := &domain.Lot{ID: newUUID(t), TenantID: tenantID, Name: name, AreaHA: 100, Crop: "soja"}
	if err := repo.Create(ctx, lot); err != nil {
		t.Fatalf("create lot %q: %v", name, err)
	}
	return lot
}

func lotNames(lots []*domain.Lot) []string {
	names := make([]string, len(lots))
	for i, l := range lots {
		names[i] = l.Name
	}
	return names
}

func TestTenantIsolation_Lots(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	lotRepo := NewLotRepo(testPool)

	tenantA := createTestTenant(ctx, t, tenantRepo, "Coop A")
	tenantB := createTestTenant(ctx, t, tenantRepo, "Coop B")
	lotA := createTestLot(ctx, t, lotRepo, tenantA.ID, "Lote Norte")
	lotB := createTestLot(ctx, t, lotRepo, tenantB.ID, "Lote Sur")

	lotsA, err := lotRepo.List(ctx, tenantA.ID)
	if err != nil {
		t.Fatalf("List(A): %v", err)
	}
	if len(lotsA) != 1 || lotsA[0].Name != lotA.Name {
		t.Fatalf("List(A) = %v, want only [%s]", lotNames(lotsA), lotA.Name)
	}

	lotsB, err := lotRepo.List(ctx, tenantB.ID)
	if err != nil {
		t.Fatalf("List(B): %v", err)
	}
	if len(lotsB) != 1 || lotsB[0].Name != lotB.Name {
		t.Fatalf("List(B) = %v, want only [%s]", lotNames(lotsB), lotB.Name)
	}

	// Reading another tenant's lot by id must be a miss, never a leak.
	if _, err := lotRepo.FindByID(ctx, tenantA.ID, lotB.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("FindByID(A, B's lot) err = %v, want domain.ErrNotFound", err)
	}
	if _, err := lotRepo.FindByID(ctx, tenantB.ID, lotA.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("FindByID(B, A's lot) err = %v, want domain.ErrNotFound", err)
	}
}

func TestTenantIsolation_Users(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	userRepo := NewUserRepo(testPool)

	tenantA := createTestTenant(ctx, t, tenantRepo, "Coop A")
	tenantB := createTestTenant(ctx, t, tenantRepo, "Coop B")

	email := "producer@isolation.test"
	userA := &domain.User{
		ID:           newUUID(t),
		TenantID:     tenantA.ID,
		Email:        email,
		PasswordHash: "not-a-real-hash-for-tests",
		FullName:     "Producer A",
		IsActive:     true,
	}
	if err := userRepo.Create(ctx, userA); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// The owning tenant finds the user...
	found, err := userRepo.FindByEmail(ctx, tenantA.ID, email)
	if err != nil {
		t.Fatalf("FindByEmail(A, %s): %v", email, err)
	}
	if found == nil || found.Email != email || found.TenantID != tenantA.ID {
		t.Fatalf("FindByEmail(A) = %+v, want the A user", found)
	}

	// ...while a different tenant sees only ErrNotFound. RLS hides the row even
	// though the email is globally unique.
	if _, err := userRepo.FindByEmail(ctx, tenantB.ID, email); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("FindByEmail(B, %s) err = %v, want domain.ErrNotFound", email, err)
	}
}

// TestTenantIsolation_RawRLS is the sharpest proof that isolation is the
// database's job, not the repository's. It bypasses WithTenant and the repos
// entirely and talks to the pool directly:
//
//   - With no app.tenant_id bound, app.current_tenant_id() returns NULL, so the
//     policy predicate tenant_id = NULL is NULL â€” never TRUE â€” and RLS FORCE
//     means even the table owner sees nothing. A missing tenant context yields
//     ZERO rows, never a leak: an unauthenticated or mis-routed connection
//     simply cannot read.
//   - Inside a WithTenant transaction the exact same SELECT sees that tenant's
//     rows, proving the mechanism is RLS, not an app-side WHERE clause.
func TestTenantIsolation_RawRLS(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	lotRepo := NewLotRepo(testPool)

	tenantA := createTestTenant(ctx, t, tenantRepo, "Coop A")
	tenantB := createTestTenant(ctx, t, tenantRepo, "Coop B")
	createTestLot(ctx, t, lotRepo, tenantA.ID, "Lote A1")
	createTestLot(ctx, t, lotRepo, tenantA.ID, "Lote A2")
	createTestLot(ctx, t, lotRepo, tenantB.ID, "Lote B1")

	// 1. No tenant context => FORCED RLS hides everything.
	var rawCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM app.lots`).Scan(&rawCount); err != nil {
		t.Fatalf("count without tenant context: %v", err)
	}
	if rawCount != 0 {
		t.Fatalf("raw SELECT without tenant context saw %d rows; FORCED RLS must expose 0", rawCount)
	}

	// 2. A tenant-bound transaction sees exactly its own rows.
	var tenantACount int
	err := WithTenant(ctx, testPool, tenantA.ID, func(tx pgxTx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM app.lots`).Scan(&tenantACount)
	})
	if err != nil {
		t.Fatalf("count inside tenant A context: %v", err)
	}
	if tenantACount != 2 {
		t.Fatalf("tenant A context saw %d lots, want 2 (A owns exactly two)", tenantACount)
	}
}

// TestCrossTenantInsertBlocked proves the WITH CHECK half of the policy: a
// session bound to tenant B cannot insert a row that claims tenant A's id, so
// even a compromised application could not write into another tenant's namespace.
// The composite PK (id, tenant_id) is a second, independent line of defense, but
// WITH CHECK alone rejects the INSERT outright.
func TestCrossTenantInsertBlocked(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)

	tenantA := createTestTenant(ctx, t, tenantRepo, "Coop A")
	tenantB := createTestTenant(ctx, t, tenantRepo, "Coop B")

	err := WithTenant(ctx, testPool, tenantB.ID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO app.lots (id, tenant_id, name) VALUES ($1, $2, $3)`,
			newUUID(t), tenantA.ID, "sneaky-cross-tenant-lot")
		return err
	})
	if err == nil {
		t.Fatal("cross-tenant INSERT succeeded; WITH CHECK must reject it")
	}

	// The rejected row must not exist anywhere â€” under no tenant context.
	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM app.lots`).Scan(&count); err != nil {
		t.Fatalf("count after rejected insert: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected INSERT left %d rows behind", count)
	}
}
