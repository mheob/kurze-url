package qr_test

import (
	"bytes"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"testing"

	goqr "github.com/piglig/go-qr"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/qr"
)

const shortURL = "https://go.kurze-url.app/sommerfest?qr=1"

// modulesWide is the rendered code's width in modules, quiet zone included.
// Computed rather than hardcoded: the module count depends on how long the
// encoded string is, and a test that hardcodes it breaks the moment the
// fixture URL changes by one character.
func modulesWide(t *testing.T, content string) int {
	t.Helper()
	code, err := goqr.EncodeText(content, goqr.Medium)
	require.NoError(t, err)
	return code.Size() + 2*qr.QuietZoneModules
}

func black() color.RGBA { return color.RGBA{A: 0xff} }
func white() color.RGBA { return color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} }

// TestRenderPNGDecodesBackToTheShortURL is the core test. Comparing bytes or
// file lengths would test piglig/go-qr; decoding tests the one property a QR
// code has to have.
func TestRenderPNGDecodesBackToTheShortURL(t *testing.T) {
	data, err := qr.Render(shortURL, qr.Options{
		Format: qr.FormatPNG, Size: 512, Foreground: black(), Background: white(),
	})
	require.NoError(t, err)

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)

	decoded, err := goqr.Decode(img)
	require.NoError(t, err)
	require.Equal(t, shortURL, decoded)
}

// TestRenderPNGDecodesInTheVereinsColours proves the colour options reach the
// pixels without breaking the scan — the whole point of enforcing contrast
// rather than refusing colour outright.
func TestRenderPNGDecodesInTheVereinsColours(t *testing.T) {
	data, err := qr.Render(shortURL, qr.Options{
		Format:     qr.FormatPNG,
		Size:       512,
		Foreground: color.RGBA{R: 0x00, G: 0x33, B: 0x66, A: 0xff},
		Background: color.RGBA{R: 0xff, G: 0xff, B: 0xf5, A: 0xff},
	})
	require.NoError(t, err)

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, color.RGBA{R: 0xff, G: 0xff, B: 0xf5, A: 0xff},
		img.At(0, 0), "the quiet zone must carry the requested background")

	decoded, err := goqr.Decode(img)
	require.NoError(t, err)
	require.Equal(t, shortURL, decoded)
}

// TestRenderPNGFitsInsideTheRequestedSize pins the ceiling semantics: go-qr
// renders whole pixels per module, so the output is the largest whole
// multiple of the module width that fits, never a rounding up past it.
func TestRenderPNGFitsInsideTheRequestedSize(t *testing.T) {
	dim := modulesWide(t, shortURL)

	for _, size := range []int{qr.MinSize, 512, qr.MaxSize} {
		data, err := qr.Render(shortURL, qr.Options{
			Format: qr.FormatPNG, Size: size, Foreground: black(), Background: white(),
		})
		require.NoError(t, err, "size %d", size)

		img, err := png.Decode(bytes.NewReader(data))
		require.NoError(t, err, "size %d", size)

		width := img.Bounds().Dx()
		require.Equal(t, width, img.Bounds().Dy(), "size %d: a QR code is square", size)
		require.LessOrEqual(t, width, size, "size %d: must never exceed the ceiling", size)
		require.Equal(t, (size/dim)*dim, width, "size %d: largest whole scale that fits", size)
	}
}

// TestRenderPNGClampsToOnePixelPerModule pins the one case where the ceiling
// cannot be honoured: a code with more modules than the requested pixels.
// Refusing would make the parameter useless, since the module count depends
// on the URL's length and no caller can know it in advance.
func TestRenderPNGClampsToOnePixelPerModule(t *testing.T) {
	long := "https://veranstaltungen.sv-gruenwald-eingetragener-verein.example/" +
		strings.Repeat("sommerfest-am-vereinsheim/", 8) + "?qr=1"
	dim := modulesWide(t, long)
	require.Greater(t, dim, qr.MinSize, "fixture must be longer than the smallest size")

	data, err := qr.Render(long, qr.Options{
		Format: qr.FormatPNG, Size: qr.MinSize, Foreground: black(), Background: white(),
	})
	require.NoError(t, err)

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, dim, img.Bounds().Dx(), "one pixel per module, not a refusal")
}

func TestRenderDefaultsToFiveHundredAndTwelve(t *testing.T) {
	dim := modulesWide(t, shortURL)

	data, err := qr.Render(shortURL, qr.Options{
		Format: qr.FormatPNG, Foreground: black(), Background: white(),
	})
	require.NoError(t, err)

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, (qr.DefaultSize/dim)*dim, img.Bounds().Dx())
}

// TestRenderSVGKeepsTheQuietZoneAndTheMatrix is the SVG counterpart of the
// decode test. The standard library has no SVG rasteriser, so the document
// cannot be handed back to goqr.Decode; what it CAN be checked against is the
// matrix both renderers share. The viewBox pins the four-module quiet zone —
// go-qr measures the SVG border in user units rather than modules, so a scale
// above 1 would silently shrink it — and one 'M' per dark module pins that
// the path carries the same matrix the PNG does.
func TestRenderSVGKeepsTheQuietZoneAndTheMatrix(t *testing.T) {
	code, err := goqr.EncodeText(shortURL, goqr.Medium)
	require.NoError(t, err)

	dark := 0
	for y := range code.Size() {
		for x := range code.Size() {
			if code.Module(x, y) {
				dark++
			}
		}
	}

	data, err := qr.Render(shortURL, qr.Options{
		Format:     qr.FormatSVG,
		Foreground: color.RGBA{R: 0x00, G: 0x33, B: 0x66, A: 0xff},
		Background: white(),
	})
	require.NoError(t, err)

	svg := string(data)
	dim := strconv.Itoa(code.Size() + 2*qr.QuietZoneModules)
	require.Contains(t, svg, `viewBox="0 0 `+dim+" "+dim+`"`)
	require.Equal(t, dark, strings.Count(svg, "M"), "one subpath per dark module")
	require.Contains(t, strings.ToLower(svg), "#003366", "the foreground must reach the path")
}

// TestRenderSVGIgnoresSize pins that a vector carries no pixel size: the
// package renders the same document whatever Size says. Refusing the
// combination is the handler's job, because only the handler can tell an
// explicit size from an absent one.
func TestRenderSVGIgnoresSize(t *testing.T) {
	small, err := qr.Render(shortURL, qr.Options{
		Format: qr.FormatSVG, Size: 64, Foreground: black(), Background: white(),
	})
	require.NoError(t, err)

	large, err := qr.Render(shortURL, qr.Options{
		Format: qr.FormatSVG, Size: 2048, Foreground: black(), Background: white(),
	})
	require.NoError(t, err)
	require.Equal(t, small, large)
}

func TestRenderRefusesALowContrastPair(t *testing.T) {
	_, err := qr.Render(shortURL, qr.Options{
		Format:     qr.FormatPNG,
		Foreground: color.RGBA{R: 0xff, G: 0xd7, A: 0xff},
		Background: white(),
	})
	require.ErrorIs(t, err, qr.ErrLowContrast)
}

func TestRenderRefusesAnUnknownFormat(t *testing.T) {
	_, err := qr.Render(shortURL, qr.Options{
		Format: qr.Format("gif"), Foreground: black(), Background: white(),
	})
	require.ErrorIs(t, err, qr.ErrInvalidFormat)
}

func TestFormatCarriesItsContentTypeAndExtension(t *testing.T) {
	require.Equal(t, "image/svg+xml", qr.FormatSVG.ContentType())
	require.Equal(t, "svg", qr.FormatSVG.Extension())
	require.Equal(t, "image/png", qr.FormatPNG.ContentType())
	require.Equal(t, "png", qr.FormatPNG.Extension())
}
