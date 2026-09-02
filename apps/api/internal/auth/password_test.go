package auth_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

func TestHashPasswordProducesAPHCEncodedArgon2idString(t *testing.T) {
	encoded, err := auth.HashPassword("correct horse battery staple")

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$"), "got %q", encoded)
	require.Len(t, strings.Split(encoded, "$"), 6)
	require.NotContains(t, encoded, "correct horse battery staple")
}

func TestHashPasswordSaltsEveryHash(t *testing.T) {
	first, err := auth.HashPassword("same password")
	require.NoError(t, err)
	second, err := auth.HashPassword("same password")
	require.NoError(t, err)

	require.NotEqual(t, first, second, "two hashes of one password must differ")
}

func TestVerifyPasswordAcceptsTheCorrectPassword(t *testing.T) {
	encoded, err := auth.HashPassword("s3cret")
	require.NoError(t, err)

	ok, err := auth.VerifyPassword(encoded, "s3cret")

	require.NoError(t, err)
	require.True(t, ok)
}

func TestVerifyPasswordRejectsTheWrongPassword(t *testing.T) {
	encoded, err := auth.HashPassword("s3cret")
	require.NoError(t, err)

	ok, err := auth.VerifyPassword(encoded, "S3cret")

	require.NoError(t, err)
	require.False(t, ok)
}

func TestVerifyPasswordRejectsAMalformedHash(t *testing.T) {
	for _, encoded := range []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=19456,t=2,p=1$",
		"$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=99$m=19456,t=2,p=1$c2FsdA$aGFzaA",
	} {
		ok, err := auth.VerifyPassword(encoded, "anything")

		require.Error(t, err, "encoded = %q", encoded)
		require.False(t, ok)
	}
}

func TestVerifyPasswordRoundTripsParametersFromTheEncodedHash(t *testing.T) {
	// A hash written with different parameters must still verify, so the
	// parameters can be raised later without invalidating existing hashes.
	encoded, err := auth.HashPasswordWithParams("legacy", auth.Params{Memory: 8192, Time: 1, Threads: 1, KeyLength: 32, SaltLength: 16})
	require.NoError(t, err)

	ok, err := auth.VerifyPassword(encoded, "legacy")

	require.NoError(t, err)
	require.True(t, ok)
}
