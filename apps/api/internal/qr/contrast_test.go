package qr_test

import (
	"image/color"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/qr"
)

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 0xff} }

func TestParseHexColorAcceptsBothSpellings(t *testing.T) {
	// The frontend sends the bare form on purpose: a raw '#' in a query
	// string is the fragment delimiter, so `?fg=#ff0000` would reach the
	// server as an empty value and silently render the default colour.
	for _, raw := range []string{"#1a2B3c", "1a2B3c"} {
		parsed, err := qr.ParseHexColor(raw)
		require.NoError(t, err, "input %q", raw)
		require.Equal(t, rgb(0x1a, 0x2b, 0x3c), parsed, "input %q", raw)
	}
}

func TestParseHexColorRefusesAnythingElse(t *testing.T) {
	for _, raw := range []string{"", "#fff", "ff00", "#gg0000", "ff000000", "rgb(1,2,3)"} {
		_, err := qr.ParseHexColor(raw)
		require.ErrorIs(t, err, qr.ErrInvalidColor, "input %q", raw)
	}
}

func TestContrastRatioMatchesWCAG(t *testing.T) {
	// Black on white is WCAG's maximum, 21:1. Any implementation that gets
	// the sRGB linearisation wrong misses this by a wide margin.
	require.InDelta(t, 21.0, qr.ContrastRatio(rgb(0, 0, 0), rgb(0xff, 0xff, 0xff)), 0.01)
	require.InDelta(t, 1.0, qr.ContrastRatio(rgb(0x33, 0x33, 0x33), rgb(0x33, 0x33, 0x33)), 0.01)
	// Order must not matter: the ratio is lighter-over-darker either way.
	require.InDelta(t,
		qr.ContrastRatio(rgb(0, 0, 0x8b), rgb(0xff, 0xff, 0xff)),
		qr.ContrastRatio(rgb(0xff, 0xff, 0xff), rgb(0, 0, 0x8b)), 0.0001)
}

func TestCheckContrastAcceptsAVereinsDarkBlueOnWhite(t *testing.T) {
	// #003366 on white is roughly 12.61:1 — the realistic Verein colour the
	// threshold must not refuse.
	require.NoError(t, qr.CheckContrast(rgb(0x00, 0x33, 0x66), rgb(0xff, 0xff, 0xff)))
}

func TestCheckContrastRefusesYellowOnWhite(t *testing.T) {
	// The failure this rule exists for: it looks fine on a monitor and does
	// not scan, and the camera says nothing about why.
	err := qr.CheckContrast(rgb(0xff, 0xd7, 0x00), rgb(0xff, 0xff, 0xff))
	require.ErrorIs(t, err, qr.ErrLowContrast)
}

// TestLowContrastTokenIsStable pins the wire contract: apps/web reads this
// exact string out of ErrorDetail.Value, so rewording it breaks the browser's
// message without breaking any Go test that does not assert on it here.
func TestLowContrastTokenIsStable(t *testing.T) {
	require.Equal(t, "low_contrast", qr.ErrLowContrast.Error())
}
