package services

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

// This file holds the pure, dependency-free core of the refresh-token scheme:
// opaque token generation, hashing and the rotation/replay decision. Keeping
// these as plain functions means the security-critical rules can be unit-tested
// without a database, a network or any framework.

// RefreshTokenDecision is the verdict of DecideRotation on a stored token.
type RefreshTokenDecision int

const (
	// RotationRejectInvalid means the token record itself is inconsistent.
	RotationRejectInvalid RefreshTokenDecision = iota
	// RotationRejectRevoked means the token was already used/rotated — replay.
	RotationRejectRevoked
	// RotationRejectExpired means the token outlived its TTL.
	RotationRejectExpired
	// RotationAllow means the token may be rotated into a successor.
	RotationAllow
)

// RefreshToken is the outcome of generating one opaque refresh token.
type RefreshToken struct {
	ID        string
	FamilyID  string
	Plain     string // the raw 256-bit token given to the client (one-time use)
	Hash      string // SHA-256 of Plain, the only thing we persist
	ExpiresAt time.Time
}

// NewRefreshToken generates a fresh opaque token with its own family. The plain
// value is cryptographically random (256 bits) and is returned to the caller
// exactly once; only the SHA-256 digest is stored.
func NewRefreshToken(ttl time.Duration) (*RefreshToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("crypto/rand: %w", err)
	}
	plain := base64.RawURLEncoding.EncodeToString(raw)

	now := time.Now()
	return &RefreshToken{
		ID:        newUUIDish(),
		FamilyID:  newUUIDish(),
		Plain:     plain,
		Hash:      HashToken(plain),
		ExpiresAt: now.Add(ttl),
	}, nil
}

// HashToken returns the SHA-256 digest of an opaque token. Storing only the
// digest means a leaked database cannot be replayed by an attacker.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newUUIDish builds a RFC 4122 v4 UUID string from crypto/rand, purely as an
// application-side identifier. The database schema relies on gen_random_uuid();
// this helper only creates idempotent application-level IDs for refresh tokens
// before they are persisted.
func newUUIDish() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error()) // unreachable in practice
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// DecideRotation is the pure heart of rotation + replay detection. Given the
// state of a stored record and the current time it answers one of the decisions
// above.
//
// The threat model: an attacker who steals a token races the legitimate user.
// Whoever refreshes first rotates; whoever is second presents an already-revoked
// token (RevokedAt set AND ReplacedBy set) and triggers family-wide revocation,
// which also kills the attacker's new token.
func DecideRotation(revoked bool, replacedBy string, expiresAt, now int64) RefreshTokenDecision {
	if revoked {
		// Any revoked token is rejected; if it was replaced, this is the replay
		// signature that must escalate to family revocation by the caller.
		if replacedBy != "" {
			return RotationRejectRevoked
		}
		return RotationRejectInvalid
	}
	if expiresAt <= now {
		return RotationRejectExpired
	}
	return RotationAllow
}
