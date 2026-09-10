package qr

import (
	"errors"
	"fmt"

	goqr "github.com/piglig/go-qr"
)

// The size bounds are a memory limit, not a style rule. At 10000 pixels the
// RGBA buffer alone is 400 MB, on a function that does not have it; 2048
// covers A4 at 300 dpi with room to spare. The floor is where this stops
// being cosmetic: below roughly two pixels per module a phone camera cannot
// resolve the modules at all.
const (
	MinSize     = 64
	MaxSize     = 2048
	DefaultSize = 512
)

// QuietZoneModules is the standard four-module margin. It is not configurable
// here: without a logo eating modules there is nothing to trade it against,
// and a caller who changes it is far likelier to produce a code that does not
// scan than one that scans better.
const QuietZoneModules = 4

// errorCorrection is fixed for the same reason. It comes back configurable
// with the logo, which is what makes the budget worth spending.
const errorCorrection = goqr.Medium

// Format is the wire value of the format query parameter.
type Format string

// The two formats this endpoint renders. SVG is the default: the primary use
// is print — a flyer, a poster, a newsletter — where a vector scales without
// a decision and a raster does not.
const (
	FormatSVG Format = "svg"
	FormatPNG Format = "png"
)

// ErrInvalidFormat is returned for any Format that is neither of the two.
var ErrInvalidFormat = errors.New("invalid_format")

// ContentType is the media type this format is served as.
func (f Format) ContentType() string {
	switch f {
	case FormatSVG:
		return "image/svg+xml"
	case FormatPNG:
		return "image/png"
	default:
		return ""
	}
}

// Extension is the filename suffix this format is downloaded with.
func (f Format) Extension() string {
	switch f {
	case FormatSVG, FormatPNG:
		return string(f)
	default:
		return ""
	}
}

// Options configures one render. Size is in pixels and applies to PNG only;
// zero means DefaultSize. A vector has no pixel size, so SVG ignores it —
// refusing an explicit size on an SVG request is the HTTP layer's job,
// because only it can distinguish an explicit value from an absent one.
type Options struct {
	Format     Format
	Size       int
	Foreground ColorRGBA
	Background ColorRGBA
}

// Render encodes content as a QR code and returns the image bytes.
//
// It renders through go-qr's in-memory ToPNGBytes/ToSVGBytes, never its
// file-writing PNG/SVG pair: a Vercel function's filesystem is read-only
// outside /tmp, so a file round trip would be a bug that only appears in
// production.
//
// Contrast is enforced here rather than left to the caller, so there is one
// enforcement point and no way to render a pair a camera cannot read by
// forgetting a check.
func Render(content string, opts Options) ([]byte, error) {
	if opts.Format.ContentType() == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidFormat, string(opts.Format))
	}
	if err := CheckContrast(opts.Foreground, opts.Background); err != nil {
		return nil, err
	}

	code, err := goqr.EncodeText(content, errorCorrection)
	if err != nil {
		return nil, fmt.Errorf("qr: encode: %w", err)
	}

	colours := []goqr.Option{
		goqr.WithDark(opts.Foreground),
		goqr.WithLight(opts.Background),
	}

	if opts.Format == FormatSVG {
		// Scale 1, not the PNG scale. go-qr measures the SVG border in user
		// units and does NOT multiply it by the scale (`dim = size*scale +
		// border*2`), while the PNG border is in modules (`(size + border*2)
		// * scale`). At any scale above 1 the SVG's quiet zone would shrink
		// below the four modules the standard asks for. At scale 1 one user
		// unit is one module, which is what a vector wants anyway.
		svg, err := code.ToSVGBytes(goqr.NewQrCodeImgConfig(1, QuietZoneModules, colours...))
		if err != nil {
			return nil, fmt.Errorf("qr: render svg: %w", err)
		}
		return svg, nil
	}

	raster, err := code.ToPNGBytes(
		goqr.NewQrCodeImgConfig(scaleFor(code, opts.Size), QuietZoneModules, colours...))
	if err != nil {
		return nil, fmt.Errorf("qr: render png: %w", err)
	}
	return raster, nil
}

// scaleFor picks the largest whole number of pixels per module whose output
// does not exceed size.
//
// Rounding up instead would break the memory bound MaxSize exists to enforce,
// and refusing a size that does not divide evenly would make the parameter
// useless, since the module count depends on how long the URL is and the
// caller cannot know it. A QR code is square and self-similar, so a code a
// few pixels smaller than requested is the same code.
//
// The floor of 1 is the one case where size cannot be honoured: a code with
// more modules than the requested pixels. One pixel per module is returned
// rather than an error, for the same reason — and scale 0 would be refused by
// go-qr's own config validation anyway.
func scaleFor(code *goqr.QrCode, size int) int {
	if size <= 0 {
		size = DefaultSize
	}
	scale := size / (code.Size() + 2*QuietZoneModules)
	if scale < 1 {
		return 1
	}
	return scale
}
