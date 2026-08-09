package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// audit_chain.go holds the pure, dependency-free core of the audit hash chain:
// canonical payload serialization, per-entry SHA-256 chaining and whole-chain
// verification. Keeping these as plain functions means the tamper-evidence
// rules can be unit-tested without a database, a network or any framework
// (pattern: refresh.go).

// genesisPrevHash is the prev_hash of a tenant's first audit entry: 64 hex
// zeros (spec R4). It is a constant shared by the service (Record) and the
// verifier so genesis is defined in exactly one place.
const genesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

// CanonicalizeEntry decodes a JSON payload into an any and re-marshals it, so a
// canonical byte sequence is derived from the payload's semantic value rather
// than its original key order or number formatting (spec R6). The SAME code
// path is used at insert and at verification, which makes the chain
// drift-proof: a stored payload always canonicalizes to the exact bytes that
// were hashed. An empty payload canonicalizes to the JSON null literal,
// matching what a NULL jsonb column reads back as.
func CanonicalizeEntry(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return []byte("null"), nil
	}
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// HashChainEntry computes the SHA-256 chain hash over the fixed field
// separator grammar (spec R5):
//
//	prev_hash | seq | tenant_id | actor_user_id | action | entity_type |
//	entity_id | payload_canonical | created_at (UTC, RFC3339Nano)
//
// createdAt is truncated to microsecond — the resolution of Postgres
// timestamptz — so the value round-trips through the database bit-identically
// and verification never reports a false tamper.
func HashChainEntry(prevHash string, seq int64, e domain.AuditEntry, canonical []byte) string {
	created := e.CreatedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	input := strings.Join([]string{
		prevHash,
		strconv.FormatInt(seq, 10),
		e.TenantID,
		e.ActorUserID,
		e.Action,
		e.EntityType,
		e.EntityID,
		string(canonical),
		created,
	}, "|")
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

// verifyChainEntries recomputes the chain for entries ordered by seq ascending
// and returns the seq of the FIRST broken entry, or 0 when the chain is intact
// (spec R8). A chain is broken when a stored prev_hash does not link to the
// previous entry's chain_hash, when a stored chain_hash does not match its
// recomputation from the stored row, or when seq is not contiguous.
func verifyChainEntries(entries []*domain.AuditEntry) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	prev := genesisPrevHash
	for i, e := range entries {
		if e == nil {
			return 0, fmt.Errorf("verify chain: nil entry at position %d", i)
		}
		if e.Seq != int64(i+1) {
			return e.Seq, fmt.Errorf("verify chain: seq %d at position %d breaks contiguity", e.Seq, i+1)
		}
		if e.PrevHash != prev {
			return e.Seq, fmt.Errorf("verify chain: entry %d prev_hash does not link to the previous chain_hash", e.Seq)
		}
		canon, err := CanonicalizeEntry(e.Payload)
		if err != nil {
			return e.Seq, fmt.Errorf("verify chain: canonicalize entry %d: %w", e.Seq, err)
		}
		if got := HashChainEntry(e.PrevHash, e.Seq, *e, canon); got != e.ChainHash {
			return e.Seq, fmt.Errorf("verify chain: entry %d chain_hash mismatch", e.Seq)
		}
		prev = e.ChainHash
	}
	return 0, nil
}
