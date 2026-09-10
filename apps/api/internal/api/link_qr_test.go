package api_test

import (
	"bytes"
	"image/png"
	"net/http"
	"strings"
	"testing"

	goqr "github.com/piglig/go-qr"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
)

// problemBody is the RFC 9457 shape Huma answers errors with, narrowed to the
// two fields these tests read back. Both QR refusals carry a string token, so
// Value is a string here rather than the int64 domains_test.go needs for its
// blocking-link count.
type problemBody struct {
	Errors []struct {
		Location string `json:"location"`
		Value    string `json:"value"`
	} `json:"errors"`
}

// qrPath is the endpoint under test, for a link created by the fixture.
func qrPath(linkID string, query string) string {
	if query == "" {
		return "/v1/links/" + linkID + "/qr"
	}
	return "/v1/links/" + linkID + "/qr?" + query
}

// TestLinkQRDefaultsToSVG pins the spec's chosen default: the primary use is
// print, where a vector scales without a decision.
func TestLinkQRDefaultsToSVG(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "sommerfest", "https://example.org/sommerfest")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, qrPath(created.ID.String(), ""), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "image/svg+xml", rec.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="sommerfest.svg"`,
		rec.Header().Get("Content-Disposition"))
	require.True(t, strings.HasPrefix(rec.Body.String(), "<svg "), "body: %s", rec.Body.String())
}

// TestLinkQREncodesTheShortURLWithTheScanMarker is the reason this feature is
// worth more than its size suggests: ?qr=1 is the only thing that can ever
// populate the qr_vs_regular analytics dimension, which the redirect path has
// been writing a constant into since the analytics went in.
func TestLinkQREncodesTheShortURLWithTheScanMarker(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "flyer", "https://example.org/the-destination")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		qrPath(created.ID.String(), "format=png"), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "image/png", rec.Header().Get("Content-Type"))

	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	require.NoError(t, err)
	decoded, err := goqr.Decode(img)
	require.NoError(t, err)

	require.Equal(t, created.ShortURL+"?qr=1", decoded)
	require.NotContains(t, decoded, "the-destination",
		"the code must encode the short URL, never the destination")
}

// TestLinkQRRefusesASizeOnSVG pins that the parameter is not silently
// ignored. A parameter that accepts a value and does nothing with it is a lie
// to every caller without a frontend to hide it.
func TestLinkQRRefusesASizeOnSVG(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "vector", "https://example.org/vector")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		qrPath(created.ID.String(), "format=svg&size=512"), nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	body := decode[problemBody](t, rec)
	require.Len(t, body.Errors, 1)
	require.Equal(t, "query.size", body.Errors[0].Location)
	require.Equal(t, "size_requires_png", body.Errors[0].Value)
}

func TestLinkQRRefusesALowContrastPair(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "gelb", "https://example.org/gelb")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		qrPath(created.ID.String(), "fg=ffd700&bg=ffffff"), nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	body := decode[problemBody](t, rec)
	require.Len(t, body.Errors, 1)
	require.Equal(t, "query.fg", body.Errors[0].Location)
	require.Equal(t, "low_contrast", body.Errors[0].Value)
}

// TestLinkQRAcceptsAColourWithOrWithoutAHash pins the widening the plan makes
// over the spec: a raw '#' in a query string is the fragment delimiter, so
// only the bare form survives a browser at all.
func TestLinkQRAcceptsAColourWithOrWithoutAHash(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "farben", "https://example.org/farben")

	for _, query := range []string{"fg=003366&bg=ffffff", "fg=%23003366&bg=%23ffffff"} {
		rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
			qrPath(created.ID.String(), query), nil)
		require.Equal(t, http.StatusOK, rec.Code, "query %q, body: %s", query, rec.Body.String())
	}
}

func TestLinkQRRefusesASizeOutsideTheBounds(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "grenzen", "https://example.org/grenzen")

	for _, query := range []string{"format=png&size=63", "format=png&size=2049"} {
		rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
			qrPath(created.ID.String(), query), nil)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
			"query %q, body: %s", query, rec.Body.String())
	}

	for _, query := range []string{"format=png&size=64", "format=png&size=2048"} {
		rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
			qrPath(created.ID.String(), query), nil)
		require.Equal(t, http.StatusOK, rec.Code, "query %q, body: %s", query, rec.Body.String())
	}
}

// TestLinkQRIsReadableByAViewer pins the scope choice: generating an image
// reads a link and changes nothing, so viewer is the right floor — unlike the
// password endpoints, which take editor because they decide who reaches the
// link.
func TestLinkQRIsReadableByAViewer(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "leser", "https://example.org/leser")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, qrPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestLinkQRIs404ForAnotherTeamsLink(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "fremd", "https://example.org/fremd")

	rec := f.do(t, f.otherAdmin, http.MethodGet, qrPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestLinkQRIsRateLimited(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.QRRateLimitPerMin = 1
	f.rebuildRouter()
	created := f.createLink(t, "begrenzt", "https://example.org/begrenzt")

	first := f.do(t, f.members[authz.RoleViewer], http.MethodGet, qrPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body.String())

	second := f.do(t, f.members[authz.RoleViewer], http.MethodGet, qrPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusTooManyRequests, second.Code, "body: %s", second.Body.String())
}

// TestLinkQRSurvivesAnUnreachableCache pins the fail-OPEN choice, which is
// the opposite of the password surface's and deliberately so: this limit is a
// cost control on a membership-bound read, not a security control, and a
// Redis outage taking QR downloads down with it would be a worse outcome than
// an unbounded rate among authenticated members for the length of the outage.
// Nothing else in this suite drives a limiter against a dead cache, so
// nothing else would catch a regression here.
func TestLinkQRSurvivesAnUnreachableCache(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "ohnecache", "https://example.org/ohnecache")

	// Port 1 refuses immediately on every platform this runs on, so the
	// limiter's error path is reached without a dial timeout.
	dead, err := cache.New("redis://127.0.0.1:1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = dead.Close() })
	f.deps.Cache = dead
	f.rebuildRouter()

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, qrPath(created.ID.String(), ""), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

// TestLinkQRRateLimitDisabledAtZero pins the convention every per-subject
// limit here follows except RATE_LIMIT_INVITE_GLOBAL_PER_MONTH: the limiter
// script compares `>= limit`, so without the guard a zero would refuse every
// render rather than none. allowRedirect shipped without that guard once
// (fixed in #67); a new limiter must not repeat it.
func TestLinkQRRateLimitDisabledAtZero(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.QRRateLimitPerMin = 0
	f.rebuildRouter()
	created := f.createLink(t, "unbegrenzt", "https://example.org/unbegrenzt")

	for range 3 {
		rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
			qrPath(created.ID.String(), ""), nil)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	}
}
