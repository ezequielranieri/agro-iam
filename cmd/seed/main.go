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

// demoUser is one seeded account. The password is shared plaintext ("test123")
// by convention so every role can be demoed; each account is hashed with the
// project's real Argon2id hasher, so login against the API works as-is.
type demoUser struct {
	email    string
	pass     string
	fullName string
	role     string
}

// demoCampaign is one agricultural season window.
type demoCampaign struct {
	name   string
	season string
	start  string // 2006-01-02
	end    string // 2006-01-02
}

// demoApplication is one input application on a lot within a campaign.
// operatorEmail is resolved to the seeded user id at insert time; "" => NULL
// operator_id, which the demo exercises on purpose (unattributed jobs).
type demoApplication struct {
	lotName       string
	campaignName  string
	product       string
	dose          string
	appliedAt     string // RFC3339
	operatorEmail string
	notes         string
}

// demoTenant is one tenant seeded for the isolation demo.
type demoTenant struct {
	name         string
	users        []demoUser
	lots         []string
	campaigns    []demoCampaign
	applications []demoApplication
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

	for _, t := range demoPlan() {
		tenantID, err := createTenant(ctx, pool, t.name)
		if err != nil {
			return fmt.Errorf("tenant %q: %w", t.name, err)
		}

		for _, u := range t.users {
			hash, err := hasher.Hash(u.pass)
			if err != nil {
				return fmt.Errorf("hash password: %w", err)
			}

			userID, err := createUser(ctx, pool, tenantID, u.email, hash, u.fullName)
			if err != nil {
				return fmt.Errorf("user %q: %w", u.email, err)
			}

			if err := assignRole(ctx, pool, tenantID, userID, u.role); err != nil {
				return fmt.Errorf("role %q: %w", u.role, err)
			}

			fmt.Printf("tenant=%s user=%s password=%s role=%s\n", t.name, u.email, u.pass, u.role)
		}

		for _, name := range t.lots {
			if err := createLot(ctx, pool, tenantID, name); err != nil {
				return fmt.Errorf("lot %q: %w", name, err)
			}
		}

		fmt.Printf("tenant=%s id=%s\n", t.name, tenantID)
	}
	fmt.Println("seed done")
	return nil
}

// demoPlan returns the deterministic demo dataset. It contains NO randomness
// and NO clock reads: byte-identical on every run, so re-seeding is stable,
// credentials never churn, and the dashboard charts are reproducible.
func demoPlan() []demoTenant {
	return []demoTenant{
		{
			name: "Coop La Esperanza",
			users: []demoUser{
				{email: "admin@esperanza.coop", pass: "test123", fullName: "Admin Coop La Esperanza", role: "admin"},
				{email: "agronomo@esperanza.coop", pass: "test123", fullName: "Ing. Agr. Laura Gómez", role: "agronomist"},
				{email: "productor@esperanza.coop", pass: "test123", fullName: "Productor Carlos Ruiz", role: "producer"},
				{email: "auditor@esperanza.coop", pass: "test123", fullName: "Auditor Marta Díaz", role: "auditor"},
				{email: "transportista@esperanza.coop", pass: "test123", fullName: "Transportista Juan Pérez", role: "hauler"},
			},
			lots: []string{"Lote Norte - Soja", "Lote Sur - Maiz"},
			campaigns: []demoCampaign{
				{name: "Campaña 2025/2026", season: "2025/2026", start: "2025-09-15", end: "2026-05-15"},
				{name: "Campaña 2026/2027", season: "2026/2027", start: "2026-09-15", end: "2027-05-15"},
			},
			applications: []demoApplication{
				{lotName: "Lote Norte - Soja", campaignName: "Campaña 2025/2026", product: "Glifosato 48%", dose: "3 L/ha", appliedAt: "2025-10-08T09:30:00Z", operatorEmail: "agronomo@esperanza.coop", notes: "Barbecho químico pre-siembra"},
				{lotName: "Lote Norte - Soja", campaignName: "Campaña 2025/2026", product: "Tebuconazol", dose: "0.8 L/ha", appliedAt: "2025-12-12T11:00:00Z", operatorEmail: "", notes: "Control de enfermedades foliares"},
				{lotName: "Lote Sur - Maiz", campaignName: "Campaña 2025/2026", product: "Atrazina", dose: "2 L/ha", appliedAt: "2025-11-20T08:15:00Z", operatorEmail: "productor@esperanza.coop", notes: "Pre-emergente"},
				{lotName: "Lote Sur - Maiz", campaignName: "Campaña 2025/2026", product: "Urea 46%", dose: "120 kg/ha", appliedAt: "2026-01-25T10:00:00Z", operatorEmail: "transportista@esperanza.coop", notes: "Fertilización nitrogenada"},
				{lotName: "Lote Norte - Soja", campaignName: "Campaña 2025/2026", product: "Imidacloprid", dose: "0.6 L/ha", appliedAt: "2026-02-03T16:45:00Z", operatorEmail: "agronomo@esperanza.coop", notes: "Tratamiento de semillas en surco"},
				{lotName: "Lote Sur - Maiz", campaignName: "Campaña 2025/2026", product: "Cipermetrina", dose: "0.5 L/ha", appliedAt: "2026-03-18T07:30:00Z", operatorEmail: "", notes: "Control de oruga cogollera"},
				{lotName: "Lote Norte - Soja", campaignName: "Campaña 2026/2027", product: "Azoxistrobina", dose: "1 L/ha", appliedAt: "2026-11-05T09:00:00Z", operatorEmail: "admin@esperanza.coop", notes: "Fungicida preventivo"},
				{lotName: "Lote Sur - Maiz", campaignName: "Campaña 2026/2027", product: "2,4-D Amine", dose: "1.5 L/ha", appliedAt: "2026-12-01T14:20:00Z", operatorEmail: "productor@esperanza.coop", notes: "Control de malezas de hoja ancha"},
				{lotName: "Lote Norte - Soja", campaignName: "Campaña 2026/2027", product: "Fósforo MAP", dose: "80 kg/ha", appliedAt: "2027-01-14T10:30:00Z", operatorEmail: "transportista@esperanza.coop", notes: "Fertilización de base"},
				{lotName: "Lote Sur - Maiz", campaignName: "Campaña 2026/2027", product: "Lambdacialotrina", dose: "0.4 L/ha", appliedAt: "2027-02-22T17:00:00Z", operatorEmail: "", notes: "Control de insectos tardíos"},
			},
		},
		{
			name: "Estancia El Algarrobo",
			users: []demoUser{
				{email: "admin@algarrobo.campo", pass: "test123", fullName: "Admin Estancia El Algarrobo", role: "admin"},
				{email: "agronomo@algarrobo.campo", pass: "test123", fullName: "Ing. Agr. Silvia Fernández", role: "agronomist"},
				{email: "productor@algarrobo.campo", pass: "test123", fullName: "Productor Roberto López", role: "producer"},
				{email: "auditor@algarrobo.campo", pass: "test123", fullName: "Auditor Paula Acosta", role: "auditor"},
				{email: "transportista@algarrobo.campo", pass: "test123", fullName: "Transportista Diego Torres", role: "hauler"},
			},
			lots: []string{"Lote Este - Trigo", "Lote Oeste - Cebada"},
			campaigns: []demoCampaign{
				{name: "Campaña 2025/2026", season: "2025/2026", start: "2025-09-15", end: "2026-05-15"},
				{name: "Campaña 2026/2027", season: "2026/2027", start: "2026-09-15", end: "2027-05-15"},
			},
			applications: []demoApplication{
				{lotName: "Lote Este - Trigo", campaignName: "Campaña 2025/2026", product: "Glifosato 48%", dose: "3 L/ha", appliedAt: "2025-09-28T08:00:00Z", operatorEmail: "agronomo@algarrobo.campo", notes: "Barbecho"},
				{lotName: "Lote Este - Trigo", campaignName: "Campaña 2025/2026", product: "Tebuconazol", dose: "0.7 L/ha", appliedAt: "2025-11-15T10:30:00Z", operatorEmail: "", notes: "Fusarium de la espiga"},
				{lotName: "Lote Oeste - Cebada", campaignName: "Campaña 2025/2026", product: "2,4-D Amine", dose: "1.2 L/ha", appliedAt: "2025-10-30T09:15:00Z", operatorEmail: "productor@algarrobo.campo", notes: "Malezas de hoja ancha"},
				{lotName: "Lote Oeste - Cebada", campaignName: "Campaña 2025/2026", product: "Urea 46%", dose: "100 kg/ha", appliedAt: "2026-01-08T11:00:00Z", operatorEmail: "transportista@algarrobo.campo", notes: "Fertilización nitrogenada"},
				{lotName: "Lote Este - Trigo", campaignName: "Campaña 2025/2026", product: "Azoxistrobina", dose: "0.9 L/ha", appliedAt: "2026-02-19T15:45:00Z", operatorEmail: "admin@algarrobo.campo", notes: "Preventivo de roya"},
				{lotName: "Lote Oeste - Cebada", campaignName: "Campaña 2025/2026", product: "Cipermetrina", dose: "0.5 L/ha", appliedAt: "2026-03-10T07:45:00Z", operatorEmail: "", notes: "Pulgón de la espiga"},
				{lotName: "Lote Este - Trigo", campaignName: "Campaña 2026/2027", product: "Fósforo MAP", dose: "90 kg/ha", appliedAt: "2026-10-22T09:30:00Z", operatorEmail: "agronomo@algarrobo.campo", notes: "Fertilización de base"},
				{lotName: "Lote Oeste - Cebada", campaignName: "Campaña 2026/2027", product: "Imidacloprid", dose: "0.6 L/ha", appliedAt: "2026-12-14T10:00:00Z", operatorEmail: "productor@algarrobo.campo", notes: "Pulguilla en macollaje"},
				{lotName: "Lote Este - Trigo", campaignName: "Campaña 2026/2027", product: "Lambdacialotrina", dose: "0.4 L/ha", appliedAt: "2027-01-27T16:30:00Z", operatorEmail: "transportista@algarrobo.campo", notes: "Insectos tardíos"},
				{lotName: "Lote Oeste - Cebada", campaignName: "Campaña 2026/2027", product: "Atrazina", dose: "1.8 L/ha", appliedAt: "2027-02-16T08:45:00Z", operatorEmail: "", notes: "Gramíneas anuales"},
			},
		},
	}
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

func createLot(ctx context.Context, pool *pgxpool.Pool, tenantID, name string) error {
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
