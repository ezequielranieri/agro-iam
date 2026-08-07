package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
)

// PasswordHasher implements ports.PasswordHasher with Argon2id. The parameters
// follow the OWASP recommendation for interactive logins:
//
//	memory   64 MiB (64*1024 KiB)
//	iterations 1
//	parallelism 4
//	salt 16 bytes random
//	key length 32 bytes
//
// Argon2id is memory-hard, which makes offline brute force of leaked hashes
// dramatically more expensive than bcrypt on GPUs. The encoding is the
// PHC format, so Verify() can re-derive the parameters from the string itself:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<salt-b64>$<hash-b64>
type PasswordHasher struct{}

// Argon2id parameters (OWASP). Exported as constants so tests can assert the
// format precisely.
const (
	ArgonMemory   = 64 * 1024
	ArgonTime     = 1
	ArgonThreads  = 4
	ArgonKeyLen   = 32
	ArgonSaltLen  = 16
	argonVersion  = 0x13 // argon2i v19 marker used by the PHC string
)

// NewPasswordHasher builds a hasher.
func NewPasswordHasher() *PasswordHasher {
	return &PasswordHasher{}
}

// Hash encodes a password into the PHC string described above.
func (h *PasswordHasher) Hash(password string) (string, error) {
	salt := make([]byte, ArgonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hash: crypto/rand: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, ArgonTime, ArgonMemory, ArgonThreads, ArgonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argonVersion, ArgonMemory, ArgonTime, ArgonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify checks a password against a PHC-encoded Argon2id hash. The format is
// parsed so parameters are never trusted from configuration â€” they always come
// from the stored string.
func (h *PasswordHasher) Verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("verify: not an argon2id PHC string")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argonVersion {
		return false, errors.New("verify: unsupported argon2 version")
	}

	var m, t, p int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errors.New("verify: malformed parameters")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("verify: malformed salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("verify: malformed hash")
	}

	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, nil
	}
	return true, nil
}

// ValidatePhcFormat is a tiny helper for tests: it checks the PHC prefix and
// parameters without running the KDF again.
func ValidatePhcFormat(encoded string) (memory, time_, threads int, ok bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, false
	}
	var m, t, p int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return 0, 0, 0, false
	}
	return m, t, p, true
}

var _ ports.PasswordHasher = (*PasswordHasher)(nil)
