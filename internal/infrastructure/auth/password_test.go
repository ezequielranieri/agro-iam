package auth

import "testing"

func TestPasswordHasherRoundTrip(t *testing.T) {
	h := NewPasswordHasher()

	encoded, err := h.Hash("S3cure!Pass#123")
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}

	// Format must match the documented PHC string.
	mem, iterations, threads, ok := ValidatePhcFormat(encoded)
	if !ok {
		t.Fatalf("encoded hash %q does not match the argon2id PHC format", encoded)
	}
	if mem != ArgonMemory || iterations != ArgonTime || threads != ArgonThreads {
		t.Fatalf("parameters differ: got m=%d,t=%d,p=%d, want m=%d,t=%d,p=%d",
			mem, iterations, threads, ArgonMemory, ArgonTime, ArgonThreads)
	}

	ok, err = h.Verify(encoded, "S3cure!Pass#123")
	if err != nil {
		t.Fatalf("Verify() error on correct password: %v", err)
	}
	if !ok {
		t.Fatal("Verify() rejected the correct password")
	}
}

func TestPasswordHasherRejectsWrongPassword(t *testing.T) {
	h := NewPasswordHasher()

	encoded, err := h.Hash("correct-password")
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}

	ok, err := h.Verify(encoded, "wrong-password")
	if err != nil {
		t.Fatalf("Verify() error on wrong password: %v", err)
	}
	if ok {
		t.Fatal("Verify() accepted a wrong password")
	}
}

func TestPasswordHasherRejectsTamperedHash(t *testing.T) {
	h := NewPasswordHasher()

	encoded, err := h.Hash("whatever")
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}

	// Corrupt the hash segment so Verify must fail on parsing, not on compare.
	corrupted := encoded[:len(encoded)-2] + "A="
	if _, err := h.Verify(corrupted, "whatever"); err == nil {
		t.Fatal("Verify() accepted a corrupted PHC string")
	}
}
