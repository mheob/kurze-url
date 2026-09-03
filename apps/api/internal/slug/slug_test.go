package slug_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/slug"
)

func TestGenerateProducesTheDocumentedShape(t *testing.T) {
	seen := map[string]bool{}

	for range 500 {
		got, err := slug.Generate()
		require.NoError(t, err)
		require.Len(t, got, slug.Length)

		for _, r := range got {
			require.True(t, strings.ContainsRune(slug.Alphabet, r),
				"generated slug %q contains %q, which is not in the alphabet", got, r)
		}
		seen[got] = true
	}

	require.Greater(t, len(seen), 490,
		"500 draws from a 1.1e12 space should almost never repeat; %d unique", len(seen))
}

func TestAlphabetExcludesLookalikes(t *testing.T) {
	require.Len(t, slug.Alphabet, 32)
	for _, excluded := range []rune{'0', '1', 'l', 'o'} {
		require.NotContains(t, slug.Alphabet, string(excluded),
			"%q is easy to misread on a printed flyer", string(excluded))
	}
	require.Equal(t, strings.ToLower(slug.Alphabet), slug.Alphabet,
		"slugs are case-insensitive, so the alphabet must be lowercase")
}

func TestNormalizeLowercasesAndTrims(t *testing.T) {
	require.Equal(t, "sommerfest", slug.Normalize("  SommerFest "))
	require.Equal(t, "abc", slug.Normalize("ABC"))
	require.Equal(t, "", slug.Normalize("   "))
}

func TestValidateAcceptsWhatTheSpecAllows(t *testing.T) {
	for _, ok := range []string{
		"abc",
		"sommerfest-2026",
		"jhv_2026",
		"a1b",
		strings.Repeat("a", 64),
	} {
		require.NoError(t, slug.Validate(ok), "%q must be accepted", ok)
	}
}

func TestValidateRejectsWhatTheSpecForbids(t *testing.T) {
	for name, bad := range map[string]string{
		"too short":           "ab",
		"too long":            strings.Repeat("a", 65),
		"leading hyphen":      "-abc",
		"trailing hyphen":     "abc-",
		"leading underscore":  "_abc",
		"trailing underscore": "abc_",
		"uppercase":           "Abc",
		"a slash":             "a/b",
		"a dot":               "a.b",
		"a space":             "a b",
		"non-ascii":           "grüße",
		"empty":               "",
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, slug.Validate(bad), slug.ErrMalformed)
		})
	}
}

func TestValidateRejectsReservedSlugs(t *testing.T) {
	for _, reserved := range []string{
		"health", "verify", "api", "admin", "login", "static", "assets",
		"robots.txt", "favicon.ico", "sitemap.xml", "apple-touch-icon.png",
		".well-known", "_next",
	} {
		t.Run(reserved, func(t *testing.T) {
			err := slug.Validate(reserved)
			require.Error(t, err, "%q must never become a link", reserved)
		})
	}
}

func TestHealthIsReservedBecauseTheRouterOwnsIt(t *testing.T) {
	require.ErrorIs(t, slug.Validate("health"), slug.ErrReserved,
		"/health is registered on the root router and would shadow this link")
}

func TestGeneratedSlugsAreAlwaysValid(t *testing.T) {
	for range 200 {
		got, err := slug.Generate()
		require.NoError(t, err)
		require.NoError(t, slug.Validate(got),
			"a generated slug must satisfy the same rules a custom alias does")
	}
}
