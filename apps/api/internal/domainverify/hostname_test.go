package domainverify_test

import (
	"strings"
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

	t.Run("rejects a hostname over 253 octets", func(t *testing.T) {
		// 131 single-octet labels joined by dots: 131 + 130 = 261 octets.
		// Each label is tiny, so this isolates the total-length check from
		// the per-label one.
		long := strings.Repeat("a.", 130) + "a"
		_, err := domainverify.NormalizeHostname(long, reserved)
		require.ErrorIs(t, err, domainverify.ErrMalformed)
	})

	t.Run("rejects a label over 63 octets", func(t *testing.T) {
		// One 64-octet label plus ".com": 68 octets total, well under the
		// 253 limit, so this isolates the per-label check.
		long := strings.Repeat("x", 64) + ".com"
		_, err := domainverify.NormalizeHostname(long, reserved)
		require.ErrorIs(t, err, domainverify.ErrMalformed)
	})

	t.Run("accepts a hostname at exactly the DNS length limits", func(t *testing.T) {
		// Three 63-octet labels plus a 61-octet label, joined by three dots:
		// 63*3 + 61 + 3 = 253 octets total, with 63-octet labels at their
		// own limit. An off-by-one here would reject a Verein's hostname
		// that is in fact valid.
		atLimit := strings.Repeat("a", 63) + "." + strings.Repeat("a", 63) + "." +
			strings.Repeat("a", 63) + "." + strings.Repeat("b", 61)
		require.Len(t, atLimit, 253)

		got, err := domainverify.NormalizeHostname(atLimit, reserved)
		require.NoError(t, err)
		require.Equal(t, atLimit, got)
	})
}
