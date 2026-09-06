package domainverify_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
)

func TestGenerateTokenProducesTheDocumentedShape(t *testing.T) {
	seen := map[string]bool{}

	for range 200 {
		got, err := domainverify.GenerateToken()
		require.NoError(t, err)
		require.Len(t, got, 32)
		require.Equal(t, strings.ToLower(got), got, "a TXT value is compared byte-for-byte in Check")

		for _, r := range got {
			require.True(t, strings.ContainsRune("abcdefghijklmnopqrstuvwxyz234567", r),
				"generated token %q contains %q, which is outside the base32 alphabet", got, r)
		}
		seen[got] = true
	}

	require.Greater(t, len(seen), 195, "200 draws from a 2^100 space should never repeat; %d unique", len(seen))
}

func TestGenerateTokenIsNotTheSlugGenerator(t *testing.T) {
	// The whole point of a dedicated generator: a slug is 8 characters from an
	// alphabet with four characters excluded for legibility. A verification
	// token must not share either constraint.
	got, err := domainverify.GenerateToken()
	require.NoError(t, err)
	require.NotEqual(t, 8, len(got))
}
