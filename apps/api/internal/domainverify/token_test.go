package domainverify_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
	"github.com/mheob/kurze-url/apps/api/internal/slug"
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

	require.Len(t, seen, 200, "200 draws from a 2^160 space should never repeat; %d unique", len(seen))
}

func TestGenerateTokenIsNotTheSlugGenerator(t *testing.T) {
	// The whole point of a dedicated generator: slug.Alphabet excludes l and o
	// (along with 0 and 1) because a slug is read off a printed flyer. A DNS
	// TXT value is copy-pasted, never read aloud, so that constraint does not
	// apply here — draw until one appears, proving the token alphabet really
	// does allow what the slug alphabet forbids.
	require.NotContains(t, slug.Alphabet, "l", "this test's premise depends on slug.Alphabet excluding l")
	require.NotContains(t, slug.Alphabet, "o", "this test's premise depends on slug.Alphabet excluding o")

	found := false
	for range 200 {
		got, err := domainverify.GenerateToken()
		require.NoError(t, err)
		if strings.ContainsAny(got, "lo") {
			found = true
			break
		}
	}
	require.True(t, found, "a generated token should contain l or o at least once in 200 draws")
}
