package destination_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/destination"
)

var self = []string{"kurze.url", "api.kurze.url"}

func TestValidateAcceptsOrdinaryHTTPSURLs(t *testing.T) {
	for _, ok := range []string{
		"https://example.org",
		"https://example.org/sommerfest?utm_source=flyer#anfahrt",
		"https://sub.example.org:8443/path",
		"https://xn--grsse-loa.example",
	} {
		require.NoError(t, destination.Validate(ok, self), "%q must be accepted", ok)
	}
}

func TestValidateRejectsEverySchemeButHTTPS(t *testing.T) {
	for name, raw := range map[string]string{
		"plain http":      "http://example.org",
		"javascript":      "javascript:alert(1)",
		"data":            "data:text/html,<script>alert(1)</script>",
		"file":            "file:///etc/passwd",
		"ftp":             "ftp://example.org/x",
		"mailto":          "mailto:vorstand@verein.test",
		"scheme-relative": "//example.org/x",
		"relative":        "/sommerfest",
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, destination.Validate(raw, self))
		})
	}
}

func TestValidateRejectsPrivateAndLocalAddresses(t *testing.T) {
	for name, raw := range map[string]string{
		"loopback v4":  "https://127.0.0.1/admin",
		"loopback v6":  "https://[::1]/admin",
		"private 10":   "https://10.0.0.1/",
		"private 172":  "https://172.16.4.9/",
		"private 192":  "https://192.168.1.1/",
		"link-local":   "https://169.254.169.254/latest/meta-data/",
		"unique-local": "https://[fd00::1]/",
		"multicast":    "https://224.0.0.1/",
		"unspecified":  "https://0.0.0.0/",
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, destination.Validate(raw, self), destination.ErrPrivateAddress)
		})
	}
}

func TestValidateRejectsOurOwnHostnames(t *testing.T) {
	for _, raw := range []string{
		"https://kurze.url/abcd",
		"https://KURZE.URL/abcd",
		"https://api.kurze.url/v1/links",
		"https://kurze.url:443/abcd",
	} {
		require.ErrorIs(t, destination.Validate(raw, self), destination.ErrSelfReference,
			"%q is a redirect loop", raw)
	}
}

func TestValidateRejectsAnOverlongURL(t *testing.T) {
	long := "https://example.org/" + strings.Repeat("a", destination.MaxLength)

	require.ErrorIs(t, destination.Validate(long, self), destination.ErrTooLong)
}

func TestValidateRejectsAHostlessURL(t *testing.T) {
	require.Error(t, destination.Validate("https://", self))
	require.Error(t, destination.Validate("", self))
}

func TestValidateAcceptsAPublicIPLiteral(t *testing.T) {
	require.NoError(t, destination.Validate("https://93.184.216.34/", self),
		"only private and local ranges are refused, not IP literals as such")
}
