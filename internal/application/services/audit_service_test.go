package services

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// fakeAuditRepo is an in-memory ports.AuditRepository (pattern:
// fakeLotRepo in lot_service_test.go) so the service is tested with no
// database. It mirrors the two real behaviors the service depends on:
//   - Tail returns the newest entry per tenant (nil when the tenant has none);
//   - Append enforces UNIQUE(tenant_id, seq), returning domain.ErrConflict
//     when the seq is already taken — the race the service must retry.
//
// onAppend, when set, runs before an append is stored; returning an error
// aborts the append without storing. This lets a test simulate a concurrent
// writer committing between the service's Tail and its Append.
type fakeAuditRepo struct {
	mu          sync.Mutex
	entries     []*domain.AuditEntry
	appendCalls int
	tailCalls   int
	onAppend    func(entry *domain.AuditEntry) error
}

func (f *fakeAuditRepo) Append(ctx context.Context, entry *domain.AuditEntry) error {
	f.mu.Lock()
	f.appendCalls++
	if f.onAppend != nil {
		// Release the lock before the hook: the hook may mutate entries to
		// simulate a concurrent writer committing mid-race.
		f.mu.Unlock()
		if err := f.onAppend(entry); err != nil {
			return err
		}
		f.mu.Lock()
	}
	defer f.mu.Unlock()
	for _, e := range f.entries {
		if e.TenantID == entry.TenantID && e.Seq == entry.Seq {
			return domain.ErrConflict
		}
	}
	f.entries = append(f.entries, entry)
	return nil
}

func (f *fakeAuditRepo) Tail(ctx context.Context, tenantID string) (*domain.AuditEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tailCalls++
	var tail *domain.AuditEntry
	for _, e := range f.entries {
		if e.TenantID != tenantID {
			continue
		}
		if tail == nil || e.Seq > tail.Seq {
			tail = e
		}
	}
	return tail, nil
}

func (f *fakeAuditRepo) ListByTenant(ctx context.Context, tenantID string) ([]*domain.AuditEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.AuditEntry
	for _, e := range f.entries {
		if e.TenantID == tenantID {
			out = append(out, e)
		}
	}
	return out, nil
}

var testAuditNow = time.Date(2026, 8, 7, 12, 0, 0, 987654321, time.UTC)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestAuditServiceRecordGenesis proves the genesis contract end to end through
// the service: with no tail the first entry gets seq 1, a prev_hash of 64 hex
// zeros and a chain_hash that recomputes from its own fields.
func TestAuditServiceRecordGenesis(t *testing.T) {
	repo := &fakeAuditRepo{}
	svc := &auditService{repo: repo, log: discardLogger(), now: func() time.Time { return testAuditNow }}

	payload := []byte(`{"origin":"web"}`)
	if err := svc.Record(context.Background(), "tenant-1", "user-1", "auth.login", "user", "u-1", payload, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if repo.appendCalls != 1 {
		t.Fatalf("Append calls = %d, want 1", repo.appendCalls)
	}
	e := repo.entries[0]
	if e.Seq != 1 {
		t.Fatalf("genesis seq = %d, want 1", e.Seq)
	}
	if e.PrevHash != genesisPrevHash {
		t.Fatalf("genesis prev_hash = %q, want 64 hex zeros", e.PrevHash)
	}
	if len(e.ChainHash) != 64 {
		t.Fatalf("chain_hash length = %d, want 64", len(e.ChainHash))
	}
	canon, err := CanonicalizeEntry(payload)
	if err != nil {
		t.Fatalf("CanonicalizeEntry: %v", err)
	}
	if got := HashChainEntry(e.PrevHash, e.Seq, *e, canon); got != e.ChainHash {
		t.Fatal("stored chain_hash must recompute from the entry's own fields")
	}
}

// TestAuditServiceRecordLinksToPrevious proves each new entry chains to the
// previous one: seq increments and prev_hash is the prior chain_hash.
func TestAuditServiceRecordLinksToPrevious(t *testing.T) {
	repo := &fakeAuditRepo{}
	svc := &auditService{repo: repo, log: discardLogger(), now: func() time.Time { return testAuditNow }}

	ctx := context.Background()
	if err := svc.Record(ctx, "tenant-1", "user-1", "auth.login", "user", "u-1", []byte(`{"n":1}`), ""); err != nil {
		t.Fatalf("Record 1: %v", err)
	}
	if err := svc.Record(ctx, "tenant-1", "user-2", "auth.refresh", "refresh_token", "f-2", []byte(`{"n":2}`), "info"); err != nil {
		t.Fatalf("Record 2: %v", err)
	}

	first, second := repo.entries[0], repo.entries[1]
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seqs = %d,%d want 1,2", first.Seq, second.Seq)
	}
	if second.PrevHash != first.ChainHash {
		t.Fatalf("entry 2 prev_hash does not link: got %q want entry 1 chain_hash", second.PrevHash)
	}
	canon, err := CanonicalizeEntry(second.Payload)
	if err != nil {
		t.Fatalf("CanonicalizeEntry: %v", err)
	}
	if got := HashChainEntry(second.PrevHash, second.Seq, *second, canon); got != second.ChainHash {
		t.Fatal("entry 2 chain_hash must recompute from its fields")
	}
}

// TestAuditServiceRecordEmptyActor proves an empty actor is threaded through to
// the append (the repository stores it as NULL via NULLIF) and the canonical
// hash treats it as the empty string.
func TestAuditServiceRecordEmptyActor(t *testing.T) {
	repo := &fakeAuditRepo{}
	svc := &auditService{repo: repo, log: discardLogger(), now: func() time.Time { return testAuditNow }}

	payload := []byte(`{"anonymous":true}`)
	if err := svc.Record(context.Background(), "tenant-1", "", "auth.login.failed", "user", "u-1", payload, "info"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	e := repo.entries[0]
	if e.ActorUserID != "" {
		t.Fatalf("actor = %q, want empty string threaded through", e.ActorUserID)
	}
	canon, err := CanonicalizeEntry(payload)
	if err != nil {
		t.Fatalf("CanonicalizeEntry: %v", err)
	}
	if got := HashChainEntry(e.PrevHash, e.Seq, *e, canon); got != e.ChainHash {
		t.Fatal("chain_hash with empty actor must recompute (canonical treats it as \"\")")
	}
}

// TestAuditServiceRecordTruncatesNow proves createdAt is truncated to
// microsecond — the resolution of Postgres timestamptz — so the value
// round-trips through the database without drift.
func TestAuditServiceRecordTruncatesNow(t *testing.T) {
	repo := &fakeAuditRepo{}
	svc := &auditService{repo: repo, log: discardLogger(), now: func() time.Time { return testAuditNow }}

	if err := svc.Record(context.Background(), "tenant-1", "user-1", "auth.login", "user", "u-1", []byte(`{}`), ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
	created := repo.entries[0].CreatedAt
	if created.Nanosecond()%1000 != 0 {
		t.Fatalf("createdAt nanoseconds = %d, want microsecond truncation", created.Nanosecond())
	}
	if !created.Equal(testAuditNow.Truncate(time.Microsecond)) {
		t.Fatalf("createdAt = %v, want %v", created, testAuditNow.Truncate(time.Microsecond))
	}
}

// TestAuditServiceRecordRetriesOnceOnConflict proves the 23505 race resolution:
// when Append loses a concurrent-append race (the fake injects a competing
// seq-2 row before returning ErrConflict), the service re-reads the tail and
// lands a contiguous seq.
func TestAuditServiceRecordRetriesOnceOnConflict(t *testing.T) {
	first := testChainEntry(1, genesisPrevHash, "", []byte(`{"n":1}`))
	canon, err := CanonicalizeEntry(first.Payload)
	if err != nil {
		t.Fatalf("CanonicalizeEntry: %v", err)
	}
	first.ChainHash = HashChainEntry(first.PrevHash, first.Seq, *first, canon)

	repo := &fakeAuditRepo{entries: []*domain.AuditEntry{first}}
	injected := false
	repo.onAppend = func(e *domain.AuditEntry) error {
		if injected {
			return nil
		}
		injected = true
		// A concurrent writer committed seq 2 between our Tail and Append.
		repo.mu.Lock()
		repo.entries = append(repo.entries, &domain.AuditEntry{
			TenantID:  "tenant-1",
			Seq:       2,
			ChainHash: strings.Repeat("c", 64),
		})
		repo.mu.Unlock()
		return domain.ErrConflict
	}
	svc := &auditService{repo: repo, log: discardLogger(), now: func() time.Time { return testAuditNow }}

	if err := svc.Record(context.Background(), "tenant-1", "user-1", "auth.login", "user", "u-1", []byte(`{"n":2}`), ""); err != nil {
		t.Fatalf("Record after retry: %v", err)
	}
	if repo.appendCalls != 2 {
		t.Fatalf("Append calls = %d, want 2 (one retry)", repo.appendCalls)
	}
	if repo.tailCalls != 2 {
		t.Fatalf("Tail calls = %d, want 2 (re-tail after conflict)", repo.tailCalls)
	}
	seqs := []int64{repo.entries[0].Seq, repo.entries[1].Seq, repo.entries[2].Seq}
	if seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 {
		t.Fatalf("final seqs = %v, want contiguous [1 2 3]", seqs)
	}
	last := repo.entries[2]
	if last.PrevHash != strings.Repeat("c", 64) {
		t.Fatalf("retried entry prev_hash = %q, want the competing entry's chain_hash", last.PrevHash)
	}
}

// TestAuditServiceRecordFailOpenAfterPersistentConflict proves the fail-open
// contract (spec R7): a conflict that survives the single retry is WARN-logged
// and returned to the caller, which proceeds with the main flow.
func TestAuditServiceRecordFailOpenAfterPersistentConflict(t *testing.T) {
	first := testChainEntry(1, genesisPrevHash, "", []byte(`{"n":1}`))
	canon, err := CanonicalizeEntry(first.Payload)
	if err != nil {
		t.Fatalf("CanonicalizeEntry: %v", err)
	}
	first.ChainHash = HashChainEntry(first.PrevHash, first.Seq, *first, canon)

	repo := &fakeAuditRepo{entries: []*domain.AuditEntry{first}}
	repo.onAppend = func(e *domain.AuditEntry) error { return domain.ErrConflict }

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	svc := &auditService{repo: repo, log: log, now: func() time.Time { return testAuditNow }}

	err = svc.Record(context.Background(), "tenant-1", "user-1", "auth.login", "user", "u-1", []byte(`{"n":2}`), "")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Record error = %v, want domain.ErrConflict (caller fails open)", err)
	}
	if repo.appendCalls != 2 {
		t.Fatalf("Append calls = %d, want exactly 2 (initial + one retry)", repo.appendCalls)
	}
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("persistent conflict must WARN-log, got: %s", buf.String())
	}
}

// TestAuditServiceRecordRequiresTenant proves a missing tenant context is
// rejected before any repository call.
func TestAuditServiceRecordRequiresTenant(t *testing.T) {
	repo := &fakeAuditRepo{}
	svc := &auditService{repo: repo, log: discardLogger(), now: time.Now}

	err := svc.Record(context.Background(), "", "user-1", "auth.login", "user", "u-1", nil, "")
	if !errors.Is(err, domain.ErrTenantRequired) {
		t.Fatalf("Record error = %v, want domain.ErrTenantRequired", err)
	}
	if repo.appendCalls != 0 || repo.tailCalls != 0 {
		t.Fatal("no repository call may happen without a tenant")
	}
}

// TestAuditServiceVerifyChain proves VerifyChain delegates to the pure
// verifier: an intact fake chain reports 0, a tampered one reports the first
// broken seq.
func TestAuditServiceVerifyChain(t *testing.T) {
	repo := &fakeAuditRepo{}
	svc := &auditService{repo: repo, log: discardLogger(), now: func() time.Time { return testAuditNow }}

	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		if err := svc.Record(ctx, "tenant-1", "user-1", "auth.login", "user", "u-1", []byte(`{"n":`+strconv.Itoa(i)+`}`), ""); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	broken, err := svc.VerifyChain(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("VerifyChain intact: %v", err)
	}
	if broken != 0 {
		t.Fatalf("intact chain broken seq = %d, want 0", broken)
	}

	// Simulate a database edit: the stored payload of seq 2 changes.
	repo.mu.Lock()
	repo.entries[1].Payload = []byte(`{"evil":true}`)
	repo.mu.Unlock()

	broken, err = svc.VerifyChain(ctx, "tenant-1")
	if err == nil {
		t.Fatal("tampered chain verified as intact")
	}
	if broken != 2 {
		t.Fatalf("tampered chain broken seq = %d, want 2", broken)
	}
}

// TestAuditServiceVerifyChainEmptyTenant proves an empty fake repo verifies as
// intact (no rows, no broken seq).
func TestAuditServiceVerifyChainEmptyTenant(t *testing.T) {
	svc := &auditService{repo: &fakeAuditRepo{}, log: discardLogger(), now: time.Now}
	broken, err := svc.VerifyChain(context.Background(), "tenant-empty")
	if err != nil {
		t.Fatalf("VerifyChain empty: %v", err)
	}
	if broken != 0 {
		t.Fatalf("empty chain broken seq = %d, want 0", broken)
	}
}

var _ ports.AuditRepository = (*fakeAuditRepo)(nil)
