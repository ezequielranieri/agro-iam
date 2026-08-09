package services

import (
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// The audit chain core is pure (pattern: refresh_test.go): canonical payload
// serialization, per-entry SHA-256 chaining and whole-chain verification are
// tested here with no database. The invariants under test are the tamper
// evidence: genesis (seq 1, prev = 64 hex zeros), linkage (an entry's prev_hash
// is the previous entry's chain_hash), canonical determinism (key order must
// not change the hash) and verification (a tampered row must surface as the
// first broken seq).

const testGenesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

// testChainEntry builds an entry carrying the chaining fields. createdAt is
// fixed at microsecond precision to mirror Postgres timestamptz resolution.
func testChainEntry(seq int64, prevHash, chainHash string, payload []byte) *domain.AuditEntry {
	return &domain.AuditEntry{
		TenantID:    "tenant-1",
		ActorUserID: "user-1",
		Action:      "auth.login",
		EntityType:  "user",
		EntityID:    "u-1",
		Payload:     payload,
		CreatedAt:   time.Date(2026, 8, 7, 12, 0, 0, 123456000, time.UTC),
		Seq:         seq,
		PrevHash:    prevHash,
		ChainHash:   chainHash,
	}
}

// TestCanonicalizeEntryCanonicalKeyOrder proves the drift-proof property of the
// canonical form: payloads that differ only in JSON key order must canonicalize
// to byte-identical output, so hashing is independent of the caller's key order.
func TestCanonicalizeEntryCanonicalKeyOrder(t *testing.T) {
	payloadA := []byte(`{"z":1,"a":[true,null],"n":12.50}`)
	payloadB := []byte(`{"n":12.5,"a":[true,null],"z":1}`)

	canonA, err := CanonicalizeEntry(payloadA)
	if err != nil {
		t.Fatalf("CanonicalizeEntry(A): %v", err)
	}
	canonB, err := CanonicalizeEntry(payloadB)
	if err != nil {
		t.Fatalf("CanonicalizeEntry(B): %v", err)
	}
	if string(canonA) != string(canonB) {
		t.Fatalf("canonical differs by key order:\n  A=%s\n  B=%s", canonA, canonB)
	}
	// Go's encoding/json sorts map keys: the canonical form is the sorted one.
	if string(canonA) != `{"a":[true,null],"n":12.5,"z":1}` {
		t.Fatalf("canonical = %s, want sorted key order", canonA)
	}
}

// TestCanonicalizeEntryFloat64Safe proves that number formatting differences
// (12.50 vs 12.5) collapse to a single float64 representation.
func TestCanonicalizeEntryFloat64Safe(t *testing.T) {
	canonA, err := CanonicalizeEntry([]byte(`{"amount":12.50}`))
	if err != nil {
		t.Fatalf("CanonicalizeEntry(12.50): %v", err)
	}
	canonB, err := CanonicalizeEntry([]byte(`{"amount":12.5}`))
	if err != nil {
		t.Fatalf("CanonicalizeEntry(12.5): %v", err)
	}
	if string(canonA) != string(canonB) {
		t.Fatalf("float64-safe canonicalization failed: %s != %s", canonA, canonB)
	}
	if string(canonA) != `{"amount":12.5}` {
		t.Fatalf("canonical = %s, want {\"amount\":12.5}", canonA)
	}
}

// TestCanonicalizeEntryEmptyPayloadIsNull pins the empty-payload contract: an
// empty payload canonicalizes to the JSON null literal, matching what a NULL
// jsonb column reads back as.
func TestCanonicalizeEntryEmptyPayloadIsNull(t *testing.T) {
	canon, err := CanonicalizeEntry(nil)
	if err != nil {
		t.Fatalf("CanonicalizeEntry(nil): %v", err)
	}
	if string(canon) != "null" {
		t.Fatalf("CanonicalizeEntry(nil) = %s, want null", canon)
	}
}

// TestHashChainEntryGenesis proves the genesis contract (spec R4): the first
// entry's prev_hash is 64 hex zeros and its chain_hash is a 64-hex SHA-256,
// deterministic across calls.
func TestHashChainEntryGenesis(t *testing.T) {
	entry := testChainEntry(1, testGenesisPrevHash, "", []byte(`{"origin":"web"}`))
	canon, err := CanonicalizeEntry(entry.Payload)
	if err != nil {
		t.Fatalf("CanonicalizeEntry: %v", err)
	}

	hash := HashChainEntry(entry.PrevHash, entry.Seq, *entry, canon)
	if len(hash) != 64 {
		t.Fatalf("chain hash length = %d, want 64 hex chars", len(hash))
	}
	if _, err := hex.DecodeString(hash); err != nil {
		t.Fatalf("chain hash is not valid hex: %v", err)
	}
	if hash == testGenesisPrevHash {
		t.Fatal("a non-empty chain hash must never equal the genesis zero hash")
	}
	again := HashChainEntry(entry.PrevHash, entry.Seq, *entry, canon)
	if again != hash {
		t.Fatal("hashing the same entry must be deterministic")
	}
}

// TestHashChainEntryLinksPrevious proves the linkage invariant (spec R5): the
// next entry's prev_hash must be the previous entry's chain_hash, and hashing
// with the stored prev_hash reproduces the stored chain_hash.
func TestHashChainEntryLinksPrevious(t *testing.T) {
	payload1 := []byte(`{"step":1}`)
	payload2 := []byte(`{"step":2}`)

	e1 := testChainEntry(1, testGenesisPrevHash, "", payload1)
	c1, err := CanonicalizeEntry(payload1)
	if err != nil {
		t.Fatalf("CanonicalizeEntry(1): %v", err)
	}
	h1 := HashChainEntry(e1.PrevHash, e1.Seq, *e1, c1)

	e2 := testChainEntry(2, h1, "", payload2)
	c2, err := CanonicalizeEntry(payload2)
	if err != nil {
		t.Fatalf("CanonicalizeEntry(2): %v", err)
	}
	h2 := HashChainEntry(e2.PrevHash, e2.Seq, *e2, c2)

	if e2.PrevHash != h1 {
		t.Fatalf("entry 2 prev_hash = %s, want entry 1 chain_hash %s", e2.PrevHash, h1)
	}
	// Re-hashing entry 2 with its own stored prev_hash reproduces h2 — the
	// property verification relies on.
	if again := HashChainEntry(e2.PrevHash, e2.Seq, *e2, c2); again != h2 {
		t.Fatal("entry 2 chain_hash is not reproducible from its own fields")
	}
}

// TestHashChainEntryChangesWithInput proves the hash is sensitive to every
// input dimension: a different seq must not reuse the previous hash.
func TestHashChainEntryChangesWithInput(t *testing.T) {
	e := testChainEntry(1, testGenesisPrevHash, "", []byte(`{}`))
	canon, err := CanonicalizeEntry(e.Payload)
	if err != nil {
		t.Fatalf("CanonicalizeEntry: %v", err)
	}
	atSeq1 := HashChainEntry(e.PrevHash, 1, *e, canon)
	atSeq2 := HashChainEntry(e.PrevHash, 2, *e, canon)
	if atSeq1 == atSeq2 {
		t.Fatal("changing seq must change the chain hash")
	}
}

// TestVerifyChainEntriesIntact proves an untouched chain verifies cleanly.
func TestVerifyChainEntriesIntact(t *testing.T) {
	entries := chainOf(3, t)
	broken, err := verifyChainEntries(entries)
	if err != nil {
		t.Fatalf("intact chain reported broken: %v", err)
	}
	if broken != 0 {
		t.Fatalf("intact chain broken seq = %d, want 0", broken)
	}
}

// TestVerifyChainEntriesTamperDetectsBrokenSeq proves a payload edit in the
// database surfaces as the first broken seq (spec scenario "Tamper breaks the
// chain").
func TestVerifyChainEntriesTamperDetectsBrokenSeq(t *testing.T) {
	entries := chainOf(3, t)
	// Tamper: an attacker edits the stored payload of seq 2 without touching
	// its chain_hash.
	entries[1].Payload = []byte(`{"evil":true}`)

	broken, err := verifyChainEntries(entries)
	if err == nil {
		t.Fatal("tampered chain verified as intact")
	}
	if broken != 2 {
		t.Fatalf("tampered chain broken seq = %d, want 2 (first broken)", broken)
	}
}

// TestVerifyChainEntriesBrokenPrevHashDetectsSeq proves a stored prev_hash
// edit (relinking attempt) is also caught, at the entry whose link is broken.
func TestVerifyChainEntriesBrokenPrevHashDetectsSeq(t *testing.T) {
	entries := chainOf(3, t)
	entries[2].PrevHash = strings.Repeat("f", 64)

	broken, err := verifyChainEntries(entries)
	if err == nil {
		t.Fatal("broken link verified as intact")
	}
	if broken != 3 {
		t.Fatalf("broken-link chain broken seq = %d, want 3", broken)
	}
}

// TestVerifyChainEntriesEmptyIsIntact pins the empty-chain contract: no rows
// means no broken seq.
func TestVerifyChainEntriesEmptyIsIntact(t *testing.T) {
	broken, err := verifyChainEntries(nil)
	if err != nil {
		t.Fatalf("empty chain reported broken: %v", err)
	}
	if broken != 0 {
		t.Fatalf("empty chain broken seq = %d, want 0", broken)
	}
}

// chainOf builds n correctly-chained entries (seq 1..n) with distinct payloads.
func chainOf(n int, t *testing.T) []*domain.AuditEntry {
	t.Helper()
	entries := make([]*domain.AuditEntry, 0, n)
	prev := testGenesisPrevHash
	for i := 1; i <= n; i++ {
		e := testChainEntry(int64(i), prev, "", []byte(`{"step":`+strconv.Itoa(i)+`}`))
		canon, err := CanonicalizeEntry(e.Payload)
		if err != nil {
			t.Fatalf("CanonicalizeEntry(%d): %v", i, err)
		}
		e.ChainHash = HashChainEntry(e.PrevHash, e.Seq, *e, canon)
		entries = append(entries, e)
		prev = e.ChainHash
	}
	return entries
}
