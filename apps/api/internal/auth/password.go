// Package auth owns credential verification: link passwords and Supabase JWTs.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidHash is returned when a stored hash cannot be parsed. It always
// means the stored value is wrong, never that the password was.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// Params are the Argon2id cost parameters. They are stored inside every hash,
// so raising them later does not invalidate existing passwords.
type Params struct {
	Memory     uint32
	Time       uint32
	Threads    uint8
	KeyLength  uint32
	SaltLength uint32
}

// DefaultParams follows OWASP's second recommended Argon2id configuration
// (19 MiB, 2 iterations, 1 degree of parallelism). The lower-memory variant is
// the right pick on serverless, where a 46 MiB allocation per verification
// would be a real cold-start and concurrency cost, and the tight per-link rate
// limit on the verify endpoint carries the rest of the brute-force defence.
var DefaultParams = Params{Memory: 19456, Time: 2, Threads: 1, KeyLength: 32, SaltLength: 16}

// HashPassword hashes a link password with the default parameters.
func HashPassword(plain string) (string, error) {
	return HashPasswordWithParams(plain, DefaultParams)
}

// HashPasswordWithParams hashes with explicit parameters, returning a
// PHC-format string: $argon2id$v=19$m=...,t=...,p=...$salt$hash
func HashPasswordWithParams(plain string, p Params) (string, error) {
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(plain), salt, p.Time, p.Memory, p.Threads, p.KeyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a plaintext password against a PHC-encoded hash using
// the parameters recorded in the hash itself. The comparison is constant time.
func VerifyPassword(encoded, plain string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, fmt.Errorf("%w: unsupported version %d", ErrInvalidHash, version)
	}

	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}
	if len(salt) == 0 || len(want) == 0 {
		return false, ErrInvalidHash
	}

	got := argon2.IDKey([]byte(plain), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))

	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
