// Package qr turns a short URL into a scannable QR code image. It owns the
// matrix, the two renderers and the contrast rule, and speaks no HTTP: the
// handler in internal/api decides status codes, this package decides pictures.
package qr

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
)

// MinContrastRatio is WCAG's floor for normal text, not its looser 3:1 for
// graphics. The stricter of the two is right here: a QR module is smaller
// than a glyph and is read by a phone camera in a badly lit clubhouse rather
// than by an eye on a calibrated screen.
const MinContrastRatio = 4.5

// ColorRGBA is image/color's RGBA under this package's own name. go-qr's
// WithLight/WithDark take a color.Color, so this is an alias rather than a
// new type: it saves every caller an import without adding a conversion.
type ColorRGBA = color.RGBA

// ErrInvalidColor and ErrLowContrast are the two ways a colour pair is
// refused. ErrLowContrast's message is a wire contract, not prose: the
// handler sends it as ErrorDetail.Value and apps/web keys its message off
// that token — see contrast_test.go, which pins it.
var (
	ErrInvalidColor = errors.New("invalid_color")
	ErrLowContrast  = errors.New("low_contrast")
)

// ParseHexColor reads rrggbb, with or without a leading '#', in either case.
//
// The bare form is accepted deliberately. A '#' in a query string is the
// fragment delimiter, so a caller writing `?fg=#ff0000` sends `fg=` and
// everything after the '#' never leaves the browser — which would land as
// "no colour given" and render the default, silently. Accepting the bare
// form is what lets the frontend avoid that trap entirely.
func ParseHexColor(raw string) (ColorRGBA, error) {
	digits := strings.TrimPrefix(raw, "#")
	if len(digits) != 6 {
		return ColorRGBA{}, fmt.Errorf("%w: %q is not #rrggbb", ErrInvalidColor, raw)
	}

	value, err := strconv.ParseUint(digits, 16, 32)
	if err != nil {
		return ColorRGBA{}, fmt.Errorf("%w: %q is not #rrggbb", ErrInvalidColor, raw)
	}

	return ColorRGBA{
		R: uint8(value >> 16),
		G: uint8(value >> 8),
		B: uint8(value),
		A: 0xff,
	}, nil
}

// relativeLuminance is WCAG 2.1's definition, sRGB channels linearised and
// weighted. Nothing here is adjustable — the formula is the specification's.
func relativeLuminance(c ColorRGBA) float64 {
	channel := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

// ContrastRatio is (lighter + 0.05) / (darker + 0.05), so the argument order
// does not matter: 1.0 for two identical colours, 21.0 for black on white.
func ContrastRatio(a, b ColorRGBA) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// CheckContrast refuses a pair a camera cannot resolve. Unlike the password
// policy's word list this is a closed formula: there is nothing to curate and
// nothing to drift, which is why apps/web mirrors it without the caveat the
// password mirror carries.
func CheckContrast(foreground, background ColorRGBA) error {
	if ratio := ContrastRatio(foreground, background); ratio < MinContrastRatio {
		return fmt.Errorf("%w: %.2f:1 is below the %.1f:1 a camera needs",
			ErrLowContrast, ratio, MinContrastRatio)
	}
	return nil
}
