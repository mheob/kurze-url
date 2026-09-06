package domainverify_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
)

func TestNormalizeHostname(t *testing.T) {
	reserved := []string{"api.kurze-url.app", "go.kurze-url.app"}

	t.Run("lowercases and trims", func(t *testing.T) {
		got, err := domainverify.NormalizeHostname("  Links.Verein.DE ", reserved)
		require.NoError(t, err)
		require.Equal(t, "links.verein.de", got)
	})

	t.Run("converts IDN to punycode", func(t *testing.T) {
		got, err := domainverify.NormalizeHostname("links.münchen.de", reserved)
		require.NoError(t, err)
		require.Equal(t, "links.xn--mnchen-3ya.de", got)
	})

	for _, bad := range []string{
		"https://links.verein.de",
		"links.verein.de/path",
		"links.verein.de:8443",
		"user@links.verein.de",
		"192.0.2.1",
		"localhost",
		"",
	} {
		t.Run("rejects "+bad, func(t *testing.T) {
			_, err := domainverify.NormalizeHostname(bad, reserved)
			require.ErrorIs(t, err, domainverify.ErrMalformed)
		})
	}

	t.Run("rejects an apex", func(t *testing.T) {
		// An apex cannot be a CNAME, so serving it here means A records and
		// taking the Verein's own website offline.
		_, err := domainverify.NormalizeHostname("verein.de", reserved)
		require.ErrorIs(t, err, domainverify.ErrApex)
	})

	t.Run("rejects this instance's own names", func(t *testing.T) {
		// Not load-bearing — nobody outside the maintainer can place the TXT
		// record under kurze-url.app, so such a claim could never verify.
		// Failing here is honest; failing at a check the caller could never
		// pass is not.
		for _, own := range []string{"api.kurze-url.app", "GO.kurze-url.app", "anything.vercel.app"} {
			_, err := domainverify.NormalizeHostname(own, reserved)
			require.ErrorIs(t, err, domainverify.ErrReserved, own)
		}
	})
}
