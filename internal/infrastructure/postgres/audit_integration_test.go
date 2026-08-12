// Audit chaining integration tests: they prove against a live PostgreSQL that
// the RLS fix (append inside WithTenant), the NULLIF actor mapping, the
// tamper-evident chain and the cross-tenant isolation hold end to end through
// the real repository and service.
//
// They follow the harness in integration_test.go: skipped unless
// TEST_DATABASE_URL is set, migrations applied by TestMain, tables truncated
// per test.
package postgres

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/services"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

func discardTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// createAuditActor inserts a real user for the tenant so audit entries can
// reference a valid actor_user_id (uuid FK to app.users).
func createAuditActor(ctx context.Context, t *testing.T, repo *UserRepo, tenantID, email string) *domain.User {
	t.Helper()
	user := &domain.User{
		ID:           newUUID(t),
		TenantID:     tenantID,
		Email:        email,
		PasswordHash: "not-a-real-hash-for-tests",
		FullName:     "Audit Actor",
		IsActive:     true,
	}
	if err := repo.Create(ctx, user); err != nil {
		t.Fatalf("create audit actor: %v", err)
	}
	return user
}

// TestAuditChainingAppendUnderRLSAndVerify proves the whole flow through the
// real repository + service (spec scenarios "Append under RLS succeeds",
// "Genesis entry", "Concurrent appends" residue and "Tamper breaks the chain"
// happy side): a tenant can append chained rows and its chain verifies.
func TestAuditChainingAppendUnderRLSAndVerify(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	tenantA := createTestTenant(ctx, t, tenantRepo, "Audit Coop A")

	repo := NewAuditRepo(testPool)
	userRepo := NewUserRepo(testPool)
	svc := services.NewAuditService(repo, discardTestLogger())

	// Genesis append with an empty actor and a real payload.
	if err := svc.Record(ctx, tenantA.ID, "", "auth.login", "user", "u-1", []byte(`{"origin":"web"}`), ""); err != nil {
		t.Fatalf("Record (genesis, empty actor): %v", err)
	}
	// A second append links to the first.
	actor := createAuditActor(ctx, t, userRepo, tenantA.ID, "actor@audit.test")
	if err := svc.Record(ctx, tenantA.ID, actor.ID, "auth.refresh", "refresh_token", "f-1", nil, "info"); err != nil {
		t.Fatalf("Record (link): %v", err)
	}

	entries, err := repo.ListByTenant(ctx, tenantA.ID)
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListByTenant returned %d entries, want 2", len(entries))
	}
	if entries[0].Seq != 1 {
		t.Fatalf("genesis seq = %d, want 1", entries[0].Seq)
	}
	if entries[0].PrevHash != strings.Repeat("0", 64) {
		t.Fatalf("genesis prev_hash = %q, want 64 hex zeros", entries[0].PrevHash)
	}
	if len(entries[0].ChainHash) != 64 {
		t.Fatalf("genesis chain_hash length = %d, want 64", len(entries[0].ChainHash))
	}
	if entries[1].Seq != 2 || entries[1].PrevHash != entries[0].ChainHash {
		t.Fatalf("entry 2 must link: seq=%d prev=%q, want seq=2 prev=entry1 chain", entries[1].Seq, entries[1].PrevHash)
	}

	// The stored chain verifies cleanly end to end (insert and verify use the
	// same canonicalization code path).
	broken, err := svc.VerifyChain(ctx, tenantA.ID)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if broken != 0 {
		t.Fatalf("intact chain broken seq = %d, want 0", broken)
	}
}

// TestAuditChainingAppendWithoutTenantRejected proves spec R1: an append with
// no WithTenant context is rejected by FORCE RLS and writes zero rows — the
// exact bug the repository fix addresses.
func TestAuditChainingAppendWithoutTenantRejected(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	tenantA := createTestTenant(ctx, t, tenantRepo, "Audit Coop A")

	_, err := testPool.Exec(ctx,
		`INSERT INTO app.audit_log (tenant_id, actor_user_id, action, entity_type, entity_id, payload, seq, prev_hash, chain_hash, severity)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		tenantA.ID, "user-1", "auth.login", "user", "u-1", []byte(`{}`),
		1, strings.Repeat("0", 64), strings.Repeat("a", 64), "info")
	if err == nil {
		t.Fatal("INSERT without tenant context must be rejected by FORCE RLS")
	}

	var count int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM app.audit_log`).Scan(&count); err != nil {
		t.Fatalf("count after rejected insert: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected INSERT left %d rows behind; FORCE RLS must write zero rows", count)
	}
}

// TestAuditChainingEmptyActorStoredAsNull proves spec R2: an empty actor is
// stored as SQL NULL via NULLIF, never as an empty string.
func TestAuditChainingEmptyActorStoredAsNull(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	tenantA := createTestTenant(ctx, t, tenantRepo, "Audit Coop A")

	repo := NewAuditRepo(testPool)
	svc := services.NewAuditService(repo, discardTestLogger())
	if err := svc.Record(ctx, tenantA.ID, "", "auth.login", "user", "u-1", []byte(`{}`), ""); err != nil {
		t.Fatalf("Record (empty actor): %v", err)
	}

	var actor *string
	err := WithTenant(ctx, testPool, tenantA.ID, func(tx pgxTx) error {
		return tx.QueryRow(ctx, `SELECT actor_user_id FROM app.audit_log`).Scan(&actor)
	})
	if err != nil {
		t.Fatalf("read actor_user_id: %v", err)
	}
	if actor != nil {
		t.Fatalf("empty actor stored as %q, want SQL NULL", *actor)
	}
}

// TestAuditChainingTamperBreaksVerifyChain proves the spec scenario "Tamper
// breaks the chain": editing a stored payload surfaces as the first broken seq
// through VerifyChain.
func TestAuditChainingTamperBreaksVerifyChain(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	tenantA := createTestTenant(ctx, t, tenantRepo, "Audit Coop A")

	repo := NewAuditRepo(testPool)
	userRepo := NewUserRepo(testPool)
	svc := services.NewAuditService(repo, discardTestLogger())
	actor := createAuditActor(ctx, t, userRepo, tenantA.ID, "actor@tamper.test")
	for i := 1; i <= 3; i++ {
		if err := svc.Record(ctx, tenantA.ID, actor.ID, "auth.login", "user", "u-1", []byte(`{"n":1}`), ""); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	// Tamper the tenant's own row 2 (RLS FORCE requires tenant context even to
	// UPDATE one's own rows).
	err := WithTenant(ctx, testPool, tenantA.ID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx, `UPDATE app.audit_log SET payload = '{"evil":true}'::jsonb WHERE seq = 2`)
		return err
	})
	if err != nil {
		t.Fatalf("tamper update: %v", err)
	}

	broken, err := svc.VerifyChain(ctx, tenantA.ID)
	if err == nil {
		t.Fatal("tampered chain verified as intact")
	}
	if broken != 2 {
		t.Fatalf("tampered chain broken seq = %d, want 2 (first broken)", broken)
	}
}

// TestAuditChainingCrossTenantIsolation proves the spec scenario "Cross-tenant
// isolation": tampering in tenant A's chain is invisible to tenant B.
func TestAuditChainingCrossTenantIsolation(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	tenantA := createTestTenant(ctx, t, tenantRepo, "Audit Coop A")
	tenantB := createTestTenant(ctx, t, tenantRepo, "Audit Coop B")

	repo := NewAuditRepo(testPool)
	userRepo := NewUserRepo(testPool)
	svc := services.NewAuditService(repo, discardTestLogger())
	actorA := createAuditActor(ctx, t, userRepo, tenantA.ID, "actor-a@iso.test")
	actorB := createAuditActor(ctx, t, userRepo, tenantB.ID, "actor-b@iso.test")
	for i := 1; i <= 2; i++ {
		if err := svc.Record(ctx, tenantA.ID, actorA.ID, "auth.login", "user", "a-1", []byte(`{"t":"A"}`), ""); err != nil {
			t.Fatalf("Record A%d: %v", i, err)
		}
		if err := svc.Record(ctx, tenantB.ID, actorB.ID, "auth.login", "user", "b-1", []byte(`{"t":"B"}`), ""); err != nil {
			t.Fatalf("Record B%d: %v", i, err)
		}
	}

	// Tamper A's row 2.
	err := WithTenant(ctx, testPool, tenantA.ID, func(tx pgxTx) error {
		_, err := tx.Exec(ctx, `UPDATE app.audit_log SET payload = '{"evil":true}'::jsonb WHERE seq = 2`)
		return err
	})
	if err != nil {
		t.Fatalf("tamper A: %v", err)
	}

	// B's chain is unaffected by A's tamper.
	brokenB, err := svc.VerifyChain(ctx, tenantB.ID)
	if err != nil {
		t.Fatalf("VerifyChain(B): %v", err)
	}
	if brokenB != 0 {
		t.Fatalf("B's chain broken seq = %d, want 0 (A's tamper must be invisible)", brokenB)
	}

	// A detects its own tamper.
	brokenA, err := svc.VerifyChain(ctx, tenantA.ID)
	if err == nil || brokenA != 2 {
		t.Fatalf("A's chain after tamper: broken=%d err=%v, want broken=2", brokenA, err)
	}
}

// TestAuditChainingUniqueTenantSeq proves the UNIQUE(tenant_id, seq) guard: a
// second append claiming an existing seq surfaces as domain.ErrConflict.
func TestAuditChainingUniqueTenantSeq(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	tenantA := createTestTenant(ctx, t, tenantRepo, "Audit Coop A")

	repo := NewAuditRepo(testPool)
	userRepo := NewUserRepo(testPool)
	actor := createAuditActor(ctx, t, userRepo, tenantA.ID, "actor@unique.test")
	entry := &domain.AuditEntry{
		TenantID:    tenantA.ID,
		ActorUserID: actor.ID,
		Action:      "auth.login",
		EntityType:  "user",
		EntityID:    "u-1",
		Payload:     []byte(`{}`),
		CreatedAt:   time.Now().UTC().Truncate(time.Microsecond),
		Seq:         1,
		PrevHash:    strings.Repeat("0", 64),
		ChainHash:   strings.Repeat("a", 64),
		Severity:    domain.SeverityInfo,
	}
	if err := repo.Append(ctx, entry); err != nil {
		t.Fatalf("first Append (seq 1): %v", err)
	}
	// Same tenant, same seq — must collide.
	dup := *entry
	if err := repo.Append(ctx, &dup); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate (tenant, seq) Append err = %v, want domain.ErrConflict", err)
	}
}

// TestAuditListRecentRLSAndLimit proves the AP1 read path against a live
// database: ListRecent returns the tenant's own entries newest first (ORDER BY
// seq DESC), honors the LIMIT window, and FORCE RLS keeps every other tenant's
// rows invisible — including a raw no-context count that must see zero rows.
func TestAuditListRecentRLSAndLimit(t *testing.T) {
	ctx := setupIntegrationTest(t)
	tenantRepo := NewTenantRepo(testPool)
	tenantA := createTestTenant(ctx, t, tenantRepo, "Audit Coop A")
	tenantB := createTestTenant(ctx, t, tenantRepo, "Audit Coop B")

	repo := NewAuditRepo(testPool)
	userRepo := NewUserRepo(testPool)
	svc := services.NewAuditService(repo, discardTestLogger())
	actorA := createAuditActor(ctx, t, userRepo, tenantA.ID, "actor-a@recent.test")
	actorB := createAuditActor(ctx, t, userRepo, tenantB.ID, "actor-b@recent.test")
	for i := 1; i <= 5; i++ {
		if err := svc.Record(ctx, tenantA.ID, actorA.ID, "auth.login", "user", "a-1", []byte(`{"n":1}`), ""); err != nil {
			t.Fatalf("Record A%d: %v", i, err)
		}
	}
	for i := 1; i <= 3; i++ {
		if err := svc.Record(ctx, tenantB.ID, actorB.ID, "auth.login", "user", "b-1", []byte(`{"n":1}`), ""); err != nil {
			t.Fatalf("Record B%d: %v", i, err)
		}
	}

	// Full window: newest first (seq DESC), only A's own 5 rows.
	all, err := repo.ListRecent(ctx, tenantA.ID, 100)
	if err != nil {
		t.Fatalf("ListRecent(A, 100): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("ListRecent(A, 100) = %d entries, want 5", len(all))
	}
	for i, e := range all {
		if e.Seq != int64(5-i) {
			t.Fatalf("ListRecent order[%d] seq = %d, want %d (newest first)", i, e.Seq, int64(5-i))
		}
		if e.TenantID != tenantA.ID {
			t.Fatalf("ListRecent leaked tenant %q row into A", e.TenantID)
		}
	}

	// LIMIT window: exactly the newest N, still newest first.
	two, err := repo.ListRecent(ctx, tenantA.ID, 2)
	if err != nil {
		t.Fatalf("ListRecent(A, 2): %v", err)
	}
	if len(two) != 2 || two[0].Seq != 5 || two[1].Seq != 4 {
		t.Fatalf("ListRecent(A, 2) = %+v, want newest 2 (seq 5,4)", two)
	}

	// B sees only its own rows, no spillover from A.
	allB, err := repo.ListRecent(ctx, tenantB.ID, 100)
	if err != nil {
		t.Fatalf("ListRecent(B): %v", err)
	}
	if len(allB) != 3 {
		t.Fatalf("ListRecent(B) = %d entries, want 3 (B owns exactly three)", len(allB))
	}
	for _, e := range allB {
		if e.TenantID != tenantB.ID {
			t.Fatalf("ListRecent leaked tenant %q row into B", e.TenantID)
		}
	}

	// No tenant context: FORCE RLS must expose zero rows — the repository is
	// never the isolation mechanism.
	var rawCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM app.audit_log`).Scan(&rawCount); err != nil {
		t.Fatalf("count without tenant context: %v", err)
	}
	if rawCount != 0 {
		t.Fatalf("raw SELECT without tenant context saw %d rows; FORCE RLS must expose 0", rawCount)
	}
}
