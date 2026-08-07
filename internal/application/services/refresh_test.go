package services

import (
	"testing"
	"time"
)

// The rotation decision is pure logic, so it is tested without any database or
// network. The rules under test are the security-critical replay detection.

func TestDecideRotationAllowsFreshToken(t *testing.T) {
	now := time.Now().Unix()
	if got := DecideRotation(false, "", now+3600, now); got != RotationAllow {
		t.Fatalf("fresh unrevoked unexpired token: got %v, want RotationAllow", got)
	}
}

func TestDecideRotationRejectsExpired(t *testing.T) {
	now := time.Now().Unix()
	// Expired: expires_at in the past, even though it was never revoked.
	if got := DecideRotation(false, "", now-1, now); got != RotationRejectExpired {
		t.Fatalf("expired token: got %v, want RotationRejectExpired", got)
	}
}

func TestDecideRotationRejectsRevokedWithReplacement(t *testing.T) {
	// The replay signature: revoked AND replaced means this token was already
	// rotated once, so presenting it again is an attack.
	if got := DecideRotation(true, "some-new-token-id", time.Now().Unix()+3600, time.Now().Unix()); got != RotationRejectRevoked {
		t.Fatalf("replayed token: got %v, want RotationRejectRevoked", got)
	}
}

func TestDecideRotationRejectsRevokedWithoutReplacement(t *testing.T) {
	// Revoked but never replaced (e.g. explicit logout): still not usable.
	if got := DecideRotation(true, "", time.Now().Unix()+3600, time.Now().Unix()); got != RotationRejectInvalid {
		t.Fatalf("revoked-no-replacement token: got %v, want RotationRejectInvalid", got)
	}
}

func TestHashTokenIsDeterministicAndNotReversible(t *testing.T) {
	a := HashToken("some-opaque-value")
	b := HashToken("some-opaque-value")
	if a != b {
		t.Fatal("hashing the same token must produce the same digest")
	}
	if a == "some-opaque-value" {
		t.Fatal("digest must never equal the plaintext")
	}
	c := HashToken("some-other-value")
	if a == c {
		t.Fatal("different tokens must produce different digests")
	}
}

func TestNewRefreshTokenIsOpaqueAndUnique(t *testing.T) {
	one, err := NewRefreshToken(time.Hour)
	if err != nil {
		t.Fatalf("NewRefreshToken error: %v", err)
	}
	two, err := NewRefreshToken(time.Hour)
	if err != nil {
		t.Fatalf("NewRefreshToken error: %v", err)
	}

	if one.Plain == two.Plain {
		t.Fatal("two generated tokens must not be identical")
	}
	if one.FamilyID == two.FamilyID {
		t.Fatal("two generated tokens must start distinct families")
	}
	if HashToken(one.Plain) != one.Hash {
		t.Fatal("stored hash must match SHA-256 of the plain token")
	}
	if one.ExpiresAt.Before(time.Now()) {
		t.Fatal("fresh token must not be expired")
	}
}
