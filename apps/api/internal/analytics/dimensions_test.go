package analytics_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
)

func request(t *testing.T, target, userAgent string, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestExtractDimensionsFromADesktopBrowser(t *testing.T) {
	r := request(t, "/hello",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
		map[string]string{"X-Vercel-IP-Country": "de"})

	d := analytics.ExtractDimensions(r)

	require.Equal(t, "Chrome", d.Browser)
	require.Equal(t, "macOS", d.OS)
	require.Equal(t, "desktop", d.Device)
	require.Equal(t, "DE", d.Country)
	require.Equal(t, "direct", d.Referrer)
	require.Empty(t, d.UTMSource)
	require.Equal(t, "human", d.BotStatus)
	require.Equal(t, "regular", d.Source)
}

func TestExtractDimensionsDetectsBots(t *testing.T) {
	r := request(t, "/hello",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", nil)

	require.Equal(t, "bot", analytics.ExtractDimensions(r).BotStatus)
}

func TestExtractDimensionsDetectsQRScans(t *testing.T) {
	r := request(t, "/hello?qr=1", "Mozilla/5.0", nil)

	require.Equal(t, "qr", analytics.ExtractDimensions(r).Source)
}

func TestExtractDimensionsReducesReferrerToItsHost(t *testing.T) {
	r := request(t, "/hello", "Mozilla/5.0",
		map[string]string{"Referer": "https://News.Example.ORG/some/deep/path?secret=1"})

	require.Equal(t, "news.example.org", analytics.ExtractDimensions(r).Referrer,
		"only the host is kept — the path and query could carry personal data")
}

func TestExtractDimensionsCapturesUTMSource(t *testing.T) {
	r := request(t, "/hello?utm_source=newsletter", "Mozilla/5.0", nil)

	require.Equal(t, "newsletter", analytics.ExtractDimensions(r).UTMSource)
}

func TestExtractDimensionsFallsBackToUnknown(t *testing.T) {
	r := request(t, "/hello", "", nil)

	d := analytics.ExtractDimensions(r)

	require.Equal(t, "unknown", d.Browser)
	require.Equal(t, "unknown", d.OS)
	require.Equal(t, "unknown", d.Device)
	require.Equal(t, "unknown", d.Country)
}

func TestExtractDimensionsBoundsValueLength(t *testing.T) {
	long := strings.Repeat("a", 501)
	r := request(t, "/hello?utm_source="+long, "Mozilla/5.0", nil)

	require.LessOrEqual(t, len(analytics.ExtractDimensions(r).UTMSource), 128,
		"unbounded dimension values would let anyone inflate the rollup table")
}

func TestExtractDimensionsTruncatesUTMSourceAtARuneBoundary(t *testing.T) {
	// 127 ASCII bytes followed by repeated "ü" (2 bytes each) puts byte index
	// 127 at the first byte of a multi-byte rune, so a byte-slice truncation
	// at 128 bytes cuts the rune in half.
	long := strings.Repeat("a", 127) + strings.Repeat("ü", 20)
	target := "/hello?" + url.Values{"utm_source": {long}}.Encode()
	r := request(t, target, "Mozilla/5.0", nil)

	value := analytics.ExtractDimensions(r).UTMSource

	require.LessOrEqual(t, len(value), 128)
	require.True(t, utf8.ValidString(value),
		"truncating mid-rune produces invalid UTF-8, which Postgres rejects on insert")
}

func TestRowsAlwaysIncludeTotalWithANullValue(t *testing.T) {
	r := request(t, "/hello", "Mozilla/5.0", nil)

	rows := analytics.ExtractDimensions(r).Rows()

	var total *analytics.DimensionRow
	for i := range rows {
		if rows[i].Type == "total" {
			total = &rows[i]
		}
	}

	require.NotNil(t, total)
	require.Nil(t, total.Value, "the total row's dimension_value must be null")
}

func TestRowsOmitUTMSourceWhenAbsent(t *testing.T) {
	r := request(t, "/hello", "Mozilla/5.0", nil)

	for _, row := range analytics.ExtractDimensions(r).Rows() {
		require.NotEqual(t, "utm_source", row.Type,
			"an absent utm_source must produce no row rather than an 'unknown' one")
	}
}
