# Link QR Codes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A team member can download a link's QR code as SVG or PNG in the Verein's colours, and a scan of that code is distinguishable from a normal click.

**Architecture:** A new pure `internal/qr` package owns the matrix, the two renderers and the contrast rule; it speaks no HTTP. One Huma operation, `GET /v1/links/{link_id}/qr`, authorizes through `authz.LinkViewerScope`, reads hostname and slug off the already-resolved link (no extra query), and answers with raw image bytes. The dashboard fetches the SVG once per link and restyles that document locally for the preview, calling the API a second time only for the download — so the browser never implements QR generation.

**Tech Stack:** Go 1.27 · chi + Huma v2.39.1 · `github.com/piglig/go-qr` v1.1.0 · Upstash Redis (existing `cache.Allow`) · TanStack Start (React) + Router/Query · Vitest + RTL + Storybook · Playwright

**Spec:** `docs/superpowers/specs/2026-09-09-link-qr-code-design.md`

---

## Global Constraints

Every task's requirements implicitly include this section.

- **The tenant is called `team`** in every identifier. "Verein" appears only in user-facing German copy.
- **No RLS.** Every query path filters by `team_id`; the check lives in Go. A non-member gets **404, never 403**.
- **The redirect path is the hot path.** This plan adds nothing to `GET /{slug}`.
- **Never store a full IP address.**
- **i18n from the first component.** No hardcoded user-facing string. English and German ship together, in `apps/web/src/i18n/locales/en.json` and `de.json`.
- **Accessibility is a requirement** (WCAG 2.1 AA), checked in CI at two levels: `@storybook/addon-a11y` on stories, `@axe-core/playwright` in e2e.
- **Errors use Huma's default RFC 9457 `application/problem+json`.** A typed reason travels in `huma.ErrorDetail{Location, Value}`; the free-text `detail` stays free to reword.
- **Rate-limit values:** `RATE_LIMIT_QR_PER_MIN=30`, per user, key `rl:qr:<user id>`, checked with the existing `cache.Client.Allow`. A `<= 0` guard in the handler **from the first commit** — every limiter script compares `>= limit`, so a zero refuses everything rather than nothing.
- **The QR encodes `<scheme>://<hostname>/<slug>?qr=1`** — the short URL, never the destination. The marker constant is `analytics.QRQueryParam` (`"qr"`).
- **Error-correction level is `goqr.Medium` and the quiet zone is 4 modules.** Neither is configurable in this plan.
- **Render through `ToPNGBytes` / `ToSVGBytes` only.** `go-qr`'s `PNG` / `SVG` write files, and a Vercel function's filesystem is read-only outside `/tmp` — a file round trip would be green locally and broken in production.
- **Contrast threshold is 4.5:1**, computed as WCAG computes relative-luminance contrast.
- **Size bounds are 64 to 2048 pixels, default 512, PNG only.** `size` with `format=svg` is a **422**, not a silent no-op.
- **Commits:** Conventional Commits, **max 50 characters including type and scope**. No co-author or generator footer.
- **All git writes go through GitButler (`but`).** Never `git add`, `git commit`, `git checkout`. The lane for this plan is `feat/link-qr`; `but commit -b feat/link-qr` creates it on the first commit and appends afterwards.
- **`pnpm format` before every commit that touches JS/TS/JSON/Markdown.** It runs oxfmt and **never touches Go** — Go changes need `gofmt -w` on the files you touched, and `gofmt -l ./...` must print nothing. Never bypass the Lefthook hooks.
- **`apps/web/src/routeTree.gen.ts`** is only committed when a change genuinely requires it.

### Two deliberate widenings of the spec

Both are supersets of what the spec asks for. They are called out here so a reviewer sees them as decisions rather than drift.

1. **`fg` and `bg` accept `rrggbb` with or without a leading `#`.** A raw `#` in a query string is the fragment delimiter: `?fg=#ff0000` sends `fg=` and the server sees an empty value, silently falling back to the default colour. The pattern is therefore `^#?[0-9a-fA-F]{6}$`, and the frontend always sends the bare form.
2. **`size` clamps its scale to a minimum of 1 pixel per module.** `go-qr` renders whole pixels per module, so a code whose module count already exceeds `size` cannot honour it. One pixel per module is returned instead of an error, because refusing would make the parameter unusable for a caller who cannot know the module count in advance.

### Two library facts that will bite

- **`go-qr`'s border unit differs between the two renderers.** PNG: `pixels = (modules + 2*border) * scale`. SVG: `viewBox = modules*scale + 2*border` — the border is **not** multiplied by the scale. So the SVG renderer must be configured with `NewQrCodeImgConfig(1, 4)`, giving a viewBox of `modules + 8` and a real four-module quiet zone. Configuring SVG with a scale above 1 shrinks the quiet zone below the standard and produces codes that scan badly.
- **`goqr.Decode(img image.Image) (string, error)` exists** and decodes the library's own PNG output directly. That is what the core Go test uses.

---

## File Structure

**Created**

| File | Responsibility |
| --- | --- |
| `apps/api/internal/qr/contrast.go` | Hex colour parsing, WCAG relative luminance, contrast ratio, the 4.5:1 rule and its sentinel. No QR knowledge. |
| `apps/api/internal/qr/contrast_test.go` | Tests for the above. |
| `apps/api/internal/qr/qr.go` | `Format`, `Options`, `Render`. Owns the size→scale arithmetic and the two `go-qr` configurations. No HTTP. |
| `apps/api/internal/qr/qr_test.go` | Decodes the rendered PNG; pins the SVG's viewBox, colours and module count; pins both size bounds and the scale clamp. |
| `apps/api/internal/api/link_qr.go` | The Huma operation: input/output structs, parameter validation, the rate limit, the 422 mapping. |
| `apps/api/internal/api/link_qr_test.go` | Endpoint tests: both formats, the 422s, the scope, the rate limit. |
| `apps/web/src/lib/qr-contrast.ts` | The browser mirror of the contrast formula. |
| `apps/web/src/lib/qr-contrast.test.ts` | Tests for the mirror, including the pairs the Go tests pin. |
| `apps/web/src/lib/qr-svg.ts` | `restyleQrSvg` — recolours a fetched SVG document and turns it into a data URL. No generation. |
| `apps/web/src/lib/qr-svg.test.ts` | Tests for the above. |
| `apps/web/src/components/link-qr-card.tsx` | The card: format choice, colour pickers, size control, preview, download. |
| `apps/web/src/components/link-qr-card.test.tsx` | RTL tests. |
| `apps/web/src/components/link-qr-card.stories.tsx` | One story per state; carries the a11y check. |

**Modified**

| File | Change |
| --- | --- |
| `apps/api/go.mod`, `apps/api/go.sum` | Add `github.com/piglig/go-qr v1.1.0`. |
| `apps/api/internal/config/config.go` | `QRRateLimitPerMin` + `RATE_LIMIT_QR_PER_MIN`. |
| `apps/api/.env.example` | The new variable, with what it protects, what it does not, and what `0` means. |
| `apps/api/internal/api/links.go` | Register the operation; extract `shortURL` so the QR URL cannot drift from the reported `short_url`. |
| `apps/api/internal/api/matrix_test.go` | A `get-link-qr` row at `authz.RoleViewer`. |
| `apps/api/openapi.json` | Regenerated. |
| `packages/api-client/src/generated/*` | Regenerated. |
| `apps/web/src/lib/api-errors.ts` | A `qrRejected` failure kind. |
| `apps/web/src/lib/api-errors.test.ts` | Tests for it. |
| `apps/web/src/server/links.ts` | `linkQrSvgFor/Fn` and `linkQrDownloadFor/Fn`. |
| `apps/web/src/server/links.test.ts` | Tests for them. |
| `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx` | Mount the card, wire its two calls and its error handling. |
| `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.test.ts` | Tests for the new exported handler. |
| `apps/web/src/i18n/locales/en.json`, `de.json` | New `links.qr*` keys. |
| `apps/web/e2e/links.spec.ts` | Download a code from the detail page. |
| `CLAUDE.md` | The endpoint's shape, and a new non-obvious constraint. |
| `docs/planning/06-api-design.md` | The settled, narrower parameter set. |

---

### Task 1: The contrast rule

**Files:**

- Create: `apps/api/internal/qr/contrast.go`
- Test: `apps/api/internal/qr/contrast_test.go`

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces:
  - `qr.ParseHexColor(raw string) (color.RGBA, error)` — accepts `rrggbb` and `#rrggbb`, case-insensitive; returns `qr.ErrInvalidColor` otherwise.
  - `qr.ContrastRatio(a, b color.RGBA) float64`
  - `qr.CheckContrast(foreground, background color.RGBA) error` — returns `qr.ErrLowContrast` below `qr.MinContrastRatio`.
  - `qr.ErrInvalidColor`, `qr.ErrLowContrast`, `qr.MinContrastRatio = 4.5`.
  - `qr.ErrLowContrast.Error()` is exactly `"low_contrast"` — the token that travels on the wire and that the browser reads back.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/qr/contrast_test.go`:

```go
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
	// #003366 on white is roughly 14:1 — the realistic Verein colour the
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/qr/...` Expected: FAIL — `no required module provides package .../internal/qr`.

- [ ] **Step 3: Write the implementation**

Create `apps/api/internal/qr/contrast.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/api && gofmt -l ./internal/qr && go vet ./internal/qr/... && go test ./internal/qr/...` Expected: `gofmt -l` prints nothing, vet is silent, tests PASS.

- [ ] **Step 5: Commit**

```bash
but commit -b feat/link-qr -m "feat(api): add qr colour contrast rules"
```

---

### Task 2: The renderer

**Files:**

- Create: `apps/api/internal/qr/qr.go`
- Test: `apps/api/internal/qr/qr_test.go`
- Modify: `apps/api/go.mod`, `apps/api/go.sum`

**Interfaces:**

- Consumes: `qr.CheckContrast`, `qr.ErrLowContrast` from Task 1.
- Produces:
  - `qr.Format` with `qr.FormatSVG` (`"svg"`) and `qr.FormatPNG` (`"png"`).
  - `(qr.Format).ContentType() string` — `"image/svg+xml"` / `"image/png"`.
  - `(qr.Format).Extension() string` — `"svg"` / `"png"`.
  - `qr.Options{Format Format; Size int; Foreground, Background color.RGBA}`.
  - `qr.Render(content string, opts Options) ([]byte, error)`.
  - `qr.MinSize = 64`, `qr.MaxSize = 2048`, `qr.DefaultSize = 512`, `qr.QuietZoneModules = 4`.
  - `qr.ErrInvalidFormat`.

- [ ] **Step 1: Add the dependency**

Run:

```bash
cd apps/api && go get github.com/piglig/go-qr@v1.1.0
```

Expected: `go.mod` gains `github.com/piglig/go-qr v1.1.0` in the first `require` block, `go.sum` gains its hashes.

- [ ] **Step 2: Write the failing test**

Create `apps/api/internal/qr/qr_test.go`:

```go
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd apps/api && go test ./internal/qr/...` Expected: FAIL — `undefined: qr.Render`, `undefined: qr.FormatSVG`, and the other new identifiers.

- [ ] **Step 4: Write the implementation**

Create `apps/api/internal/qr/qr.go`:

```go
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd apps/api && gofmt -l ./internal/qr && go vet ./internal/qr/... && go test ./internal/qr/...` Expected: `gofmt -l` prints nothing, vet is silent, every test PASSes.

- [ ] **Step 6: Commit**

```bash
but commit -b feat/link-qr -m "feat(api): render qr codes as svg and png"
```

---

### Task 3: The rate-limit setting

**Files:**

- Modify: `apps/api/internal/config/config.go`
- Modify: `apps/api/.env.example`

**Interfaces:**

- Consumes: nothing.
- Produces: `config.Config.QRRateLimitPerMin int`, read from `RATE_LIMIT_QR_PER_MIN`, default `30`.

- [ ] **Step 1: Add the field**

In `apps/api/internal/config/config.go`, in the rate-limit block of the `Config` struct, immediately after the `DomainVerifyPerDomainRateLimitPerHour` / `DomainVerifyPerUserRateLimitPerHour` pair, add:

```go
	// QRRateLimitPerMin caps GET /v1/links/{id}/qr per user. It is a
	// consistency measure, not a defence: rendering a QR code is a small
	// bitmap, not an Argon2id hash, and the endpoint is membership-bound.
	// Plenty of member-driven reads here carry no limit at all. What sets
	// this one apart is that its cost scales with a parameter the caller
	// chooses — a 2048-pixel render is not a 512-pixel render — and that the
	// size bound caps the peak, not the rate.
	QRRateLimitPerMin int
```

- [ ] **Step 2: Read the environment variable**

In the same file, in `Load`, immediately after the `DomainVerifyPerUserRateLimitPerHour` block, add:

```go
	if cfg.QRRateLimitPerMin, err = envInt("RATE_LIMIT_QR_PER_MIN", 30); err != nil {
		return Config{}, err
	}
```

- [ ] **Step 3: Document it**

In `apps/api/.env.example`, immediately after the `RATE_LIMIT_LINK_CREATE_PER_MIN=20` block, add:

```
# QR renders per minute, per user. GET /v1/links/{id}/qr is the only
# member-driven read whose cost scales with a parameter the caller picks: a
# 2048-pixel render allocates roughly 16 MB of RGBA where a 512-pixel one
# allocates one. The size bound caps how expensive a single call gets; this
# caps how often. Thirty a minute is far above real use — the dashboard
# fetches one SVG per link and then restyles it in the browser, so a member
# comparing colours for ten minutes still spends two requests.
#
# What it does NOT do: protect Upstash's monthly command budget, and it is
# not a security control either — the endpoint already requires membership in
# the link's team, so there is no unauthenticated caller to bound. The check
# fails OPEN: if Redis is unreachable the render still happens, because a
# cache outage taking QR downloads down with it would be a worse outcome than
# an unbounded rate among authenticated members for the length of the outage.
#
# 0 disables this axis: like every other per-subject limit in this file
# except RATE_LIMIT_INVITE_GLOBAL_PER_MONTH, it means "not enforced," not
# "refuse everything."
RATE_LIMIT_QR_PER_MIN=30
```

- [ ] **Step 4: Verify the config still loads**

Run: `cd apps/api && gofmt -l ./internal/config && go test ./internal/config/...` Expected: `gofmt -l` prints nothing, tests PASS.

- [ ] **Step 5: Commit**

```bash
but commit -b feat/link-qr -m "feat(api): add the qr rate limit setting"
```

---

### Task 4: The endpoint

**Files:**

- Create: `apps/api/internal/api/link_qr.go`
- Test: `apps/api/internal/api/link_qr_test.go`
- Modify: `apps/api/internal/api/links.go` (register the operation; extract `shortURL`)
- Modify: `apps/api/internal/api/matrix_test.go` (one new row)

**Interfaces:**

- Consumes: `qr.Render`, `qr.Options`, `qr.Format`, `qr.FormatSVG`, `qr.FormatPNG`, `qr.ParseHexColor`, `qr.ErrLowContrast`, `qr.ErrInvalidColor`, `qr.MinSize`, `qr.MaxSize`, `qr.DefaultSize` (Tasks 1–2); `config.Config.QRRateLimitPerMin` (Task 3); `authz.LinkViewerScope` with its `Member()` and `Link()` accessors, where `authz.ResolvedLink` already carries `Hostname` and `Slug`.
- Produces: operation `get-link-qr`, `GET /v1/links/{link_id}/qr`; `Deps.shortURL(hostname, slug string) string`.

**Read before writing:** `apps/api/internal/api/link_password.go` for the handler shape and the rate-limit helper shape, and `apps/api/internal/api/links.go:75-96` for `linkResponse`.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/api/link_qr_test.go`:

```go
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
```

Add the problem-body helper at the top of the same file. No such type exists in the package's tests yet — `domains_test.go` decodes an inline anonymous struct with an `int64` value, which does not fit a string token — so this is new, and it belongs in this file:

```go
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
```

- [ ] **Step 2: Add the matrix row**

In `apps/api/internal/api/matrix_test.go`, in `teamScopedCases`, immediately after the `remove-link-password` entry, add:

```go
	{"get-link-qr", http.MethodGet, "/v1/links/{link}/qr", nil, authz.RoleViewer},
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'TestLinkQR|TestEveryOperationIsAccountedFor' -v 2>&1 | tail -30` Expected: FAIL — the QR tests get 404 from chi (no such route), and `TestEveryOperationIsAccountedFor` fails on a matrix row naming an operation that is not registered.

- [ ] **Step 4: Extract the short-URL composition**

In `apps/api/internal/api/links.go`, replace the `ShortURL:` line inside `linkResponse`:

```go
		ShortURL:         fmt.Sprintf("%s://%s/%s", d.Config.ShortURLScheme, r.Hostname, r.Slug),
```

with:

```go
		ShortURL:         d.shortURL(r.Hostname, r.Slug),
```

and add, immediately above `linkResponse`:

```go
// shortURL composes a link's public address. It has two callers — the API
// response below and the QR generator — and it is one function so a code
// printed on two hundred flyers can never encode an address the dashboard
// does not report.
func (d Deps) shortURL(hostname, slug string) string {
	return fmt.Sprintf("%s://%s/%s", d.Config.ShortURLScheme, hostname, slug)
}
```

- [ ] **Step 5: Register the operation**

In `apps/api/internal/api/links.go`, inside `registerLinks`, immediately after the `remove-link-password` registration and before the closing brace, add:

```go
	huma.Register(api, huma.Operation{
		OperationID: "get-link-qr",
		Method:      http.MethodGet,
		Path:        "/v1/links/{link_id}/qr",
		Summary:     "Download a link's QR code",
		Tags:        []string{"Links"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
		// Declared by hand because this is the first /v1 operation that
		// answers with something other than JSON. processOutputType only
		// invents an application/json entry when Responses["200"].Content is
		// empty, so pre-populating it here is what keeps the generated
		// TypeScript client from typing a PNG as a JSON body and trying to
		// parse it.
		Responses: map[string]*huma.Response{
			"200": {
				Description: "The QR code image, as SVG or PNG.",
				Content: map[string]*huma.MediaType{
					"image/svg+xml": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
					"image/png":     {Schema: &huma.Schema{Type: "string", Format: "binary"}},
				},
			},
		},
	}, d.getLinkQR)
```

- [ ] **Step 6: Write the handler**

Create `apps/api/internal/api/link_qr.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/qr"
)

// LinkQRInput declares its authorization in its type: LinkViewerScope, not the
// LinkEditorScope the password routes take. Generating an image reads a link
// and changes nothing, so the viewer role is the right floor.
//
// Size carries no default: Huma substitutes a `default:` tag before the
// handler sees the value, which would make an explicit ?size=512 on an SVG
// request indistinguishable from no size at all — and refusing that
// combination is the whole point. Zero therefore means "absent", and
// qr.DefaultSize is applied in the handler. Absent parameters skip validation
// entirely (huma.go returns before Validate when the raw value is empty), so
// the minimum below never fires on a request that omits it.
type LinkQRInput struct {
	authz.LinkViewerScope
	Format string `query:"format" enum:"svg,png" doc:"svg (the default) for print, png for screens."`
	Size   int    `query:"size" minimum:"64" maximum:"2048" doc:"Pixels, PNG only; 512 by default. A ceiling, not an exact dimension: the code is rendered at the largest whole number of pixels per module that fits, so the result can be up to one module narrower than asked for. A QR code is square and self-similar, so that code is the same code."`
	FG     string `query:"fg" pattern:"^#?[0-9a-fA-F]{6}$" patternDescription:"rrggbb, with or without a leading #" doc:"The colour of the code itself. Black by default. Send it without the '#': a raw '#' in a query string is the fragment delimiter and never reaches the server."`
	BG     string `query:"bg" pattern:"^#?[0-9a-fA-F]{6}$" patternDescription:"rrggbb, with or without a leading #" doc:"The background colour. White by default."`
}

// LinkQROutput answers with raw image bytes.
//
// Huma writes a []byte Body straight to the response and returns before its
// marshalling path, which is also the path that would set a Content-Type — so
// the media type has to travel as a response header field on this struct
// instead. Content-Disposition carries the slug because a Verein downloading
// codes for six links should not end up with qr.png beside qr(3).png and no
// way to tell which one is the Sommerfest.
type LinkQROutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

// getLinkQR is GET /v1/links/{link_id}/qr.
//
// It issues no query of its own: LinkViewerScope has already resolved the
// link, and authz.ResolvedLink carries the hostname and slug this needs. It
// writes nothing either — a download is a read, so there is no audit entry
// here, the same as every other read on this surface.
func (d Deps) getLinkQR(ctx context.Context, in *LinkQRInput) (*LinkQROutput, error) {
	if err := d.allowQR(ctx, in.Member().UserID); err != nil {
		return nil, err
	}

	format := qr.Format(in.Format)
	if in.Format == "" {
		format = qr.FormatSVG
	}

	// A vector has no pixel size, so a size on an SVG request is refused
	// rather than ignored. The dashboard additionally hides the control when
	// SVG is selected — that is a different job: the API protects callers
	// without a frontend (the CLI, a script, the generated client), the
	// interface protects a person from a control whose movement has no
	// effect. Both, not either.
	if format == qr.FormatSVG && in.Size != 0 {
		return nil, huma.Error422UnprocessableEntity(
			"size applies to PNG only; an SVG has no pixel size",
			&huma.ErrorDetail{Location: "query.size", Value: "size_requires_png"})
	}

	foreground, err := parseQRColor(in.FG, "query.fg", qr.ColorRGBA{A: 0xff})
	if err != nil {
		return nil, err
	}
	background, err := parseQRColor(in.BG, "query.bg", qr.ColorRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	if err != nil {
		return nil, err
	}

	link := in.Link()
	content := d.shortURL(link.Hostname, link.Slug) + "?" + analytics.QRQueryParam + "=1"

	image, err := qr.Render(content, qr.Options{
		Format:     format,
		Size:       in.Size,
		Foreground: foreground,
		Background: background,
	})
	switch {
	case errors.Is(err, qr.ErrLowContrast):
		// Keyed on query.fg rather than query.bg because the code's own
		// colour is the one a Verein sets deliberately; the background is
		// usually left white. Same typed-value convention deleteDomain
		// established and the password policy follows.
		return nil, huma.Error422UnprocessableEntity(
			"the two colours are too close together for a camera to read the code",
			&huma.ErrorDetail{Location: "query.fg", Value: qr.ErrLowContrast.Error()})
	case err != nil:
		d.Log.Error("render link qr", "error", err, "link_id", link.ID)
		return nil, huma.Error500InternalServerError("could not render the QR code")
	}

	return &LinkQROutput{
		ContentType: format.ContentType(),
		ContentDisposition: fmt.Sprintf("attachment; filename=%q",
			link.Slug+"."+format.Extension()),
		Body: image,
	}, nil
}

// parseQRColor turns one colour parameter into a value, falling back to
// fallback when the caller sent nothing. The pattern on the field already
// refuses anything that is not rrggbb, so the error branch is defence in
// depth rather than the expected path — but it must not become a silent
// default, which is exactly how the missing-'#' trap would go unnoticed.
func parseQRColor(raw, location string, fallback qr.ColorRGBA) (qr.ColorRGBA, error) {
	if raw == "" {
		return fallback, nil
	}
	parsed, err := qr.ParseHexColor(raw)
	if err != nil {
		return qr.ColorRGBA{}, huma.Error422UnprocessableEntity(
			"colours must be given as rrggbb",
			&huma.ErrorDetail{Location: location, Value: qr.ErrInvalidColor.Error()})
	}
	return parsed, nil
}

// allowQR caps renders per user.
//
// It fails OPEN, like allowLinkCreate and allowRedirect and unlike the
// password surface. This limit is a cost control, not a security control: the
// endpoint is already membership-bound, so there is no unauthenticated caller
// to refuse, and letting a Redis outage take QR downloads down with it would
// be a worse outcome than an unbounded rate among authenticated members for
// the length of the outage.
//
// The <= 0 guard is the codebase's convention for "axis not enforced" and is
// here from the first commit: Allow's script compares `>= limit`, so without
// it a zero would refuse every render rather than none. The nil-Cache guard
// is the local-development affordance only; REDIS_URL is required in every
// deployed environment.
func (d Deps) allowQR(ctx context.Context, userID uuid.UUID) error {
	if d.Cache == nil || d.Config.QRRateLimitPerMin <= 0 {
		return nil
	}

	ok, _, err := d.Cache.Allow(ctx,
		"rl:qr:"+userID.String(), d.Config.QRRateLimitPerMin, time.Minute)
	if err != nil {
		d.Log.Error("qr rate limit check failed, failing open", "error", err)
		return nil
	}
	if !ok {
		return huma.Error429TooManyRequests("too many QR codes; try again shortly")
	}
	return nil
}
```

Note the import list above deliberately has **no** `net/http`: this handler names no status code itself — `huma.Error422UnprocessableEntity` and friends carry them — so adding it would not compile.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd apps/api && gofmt -l ./internal && go vet ./... && go test ./internal/api/ -run 'TestLinkQR|TestRolePermissionMatrix|TestEveryOperationIsAccountedFor'` Expected: `gofmt -l` prints nothing, vet is silent, every test PASSes.

- [ ] **Step 8: Run the whole API suite**

Run: `cd apps/api && go test ./...` Expected: PASS.

- [ ] **Step 9: Commit**

```bash
but commit -b feat/link-qr -m "feat(api): add the link qr endpoint"
```

---

### Task 5: The OpenAPI document and the generated client

**Files:**

- Modify: `apps/api/openapi.json` (regenerated)
- Modify: `packages/api-client/src/generated/sdk.gen.ts`, `types.gen.ts` (regenerated)

**Interfaces:**

- Consumes: the `get-link-qr` operation from Task 4.
- Produces: `getLinkQr` and its `GetLinkQrData` / `GetLinkQrResponses` types, exported from `@kurze-url/api-client`.

- [ ] **Step 1: Regenerate**

Run: `pnpm generate:api` Expected: `apps/api/openapi.json` and `packages/api-client/src/generated/*` change; oxfmt runs as the script's last step.

- [ ] **Step 2: Verify the document declares both image media types**

Run:

```bash
node -e "const d=require('./apps/api/openapi.json');console.log(JSON.stringify(d.paths['/v1/links/{link_id}/qr'].get.responses['200'],null,2))"
```

Expected: a `content` object with exactly `image/svg+xml` and `image/png` keys, and **no** `application/json` key. If `application/json` is present, the hand-written `Responses` map in Task 4 did not survive — fix that before continuing, because the generated client would then parse a PNG as JSON.

- [ ] **Step 3: Verify the generated SDK function exists and note its response type**

Run:

```bash
grep -n "getLinkQr" packages/api-client/src/generated/sdk.gen.ts
grep -n "GetLinkQr" packages/api-client/src/generated/types.gen.ts | head -20
```

Expected: `getLinkQr` is exported from `sdk.gen.ts`, and `types.gen.ts` carries `GetLinkQrData` plus a response type. Record the exact response body type in the task report — Task 7's normalizer is written to accept `Blob`, `File` or `string` at runtime precisely so it does not depend on which of those the generator chose, but the report should say which it is.

- [ ] **Step 4: Typecheck**

Run: `pnpm typecheck && pnpm lint` Expected: both clean.

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/link-qr -m "chore: regenerate the api client for qr"
```

---

### Task 6: The browser's contrast mirror, the restyler, and the error kind

**Files:**

- Create: `apps/web/src/lib/qr-contrast.ts`
- Create: `apps/web/src/lib/qr-contrast.test.ts`
- Create: `apps/web/src/lib/qr-svg.ts`
- Create: `apps/web/src/lib/qr-svg.test.ts`
- Modify: `apps/web/src/lib/api-errors.ts`
- Modify: `apps/web/src/lib/api-errors.test.ts`

**Interfaces:**

- Consumes: nothing from earlier tasks at runtime; mirrors Task 1's formula and Task 4's wire tokens.
- Produces:
  - `MIN_QR_CONTRAST_RATIO = 4.5`, `qrContrastRatio(a: string, b: string): number`, `hasEnoughQrContrast(foreground: string, background: string): boolean` from `lib/qr-contrast.ts`.
  - `restyleQrSvg(svg: string, options: { background: string; foreground: string }): string` and `qrSvgDataUrl(svg: string): string` from `lib/qr-svg.ts`.
  - `ApiFailure` gains `{ kind: 'qrRejected'; reason: QrRejectionReason | 'rejected' }`, with `export type QrRejectionReason = 'invalid_color' | 'low_contrast' | 'size_requires_png'`.

- [ ] **Step 1: Write the failing contrast test**

Create `apps/web/src/lib/qr-contrast.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { hasEnoughQrContrast, MIN_QR_CONTRAST_RATIO, qrContrastRatio } from './qr-contrast';

describe('qrContrastRatio', () => {
	it('matches WCAG at both extremes', () => {
		expect(qrContrastRatio('#000000', '#ffffff')).toBeCloseTo(21, 2);
		expect(qrContrastRatio('#333333', '#333333')).toBeCloseTo(1, 2);
	});

	it('does not depend on the argument order', () => {
		expect(qrContrastRatio('#00008b', '#ffffff')).toBeCloseTo(
			qrContrastRatio('#ffffff', '#00008b'),
			6,
		);
	});

	it('reads a colour with or without the leading hash', () => {
		expect(qrContrastRatio('000000', 'ffffff')).toBeCloseTo(21, 2);
	});
});

describe('hasEnoughQrContrast', () => {
	/**
	 * The same two pairs `apps/api/internal/qr/contrast_test.go` pins, so a
	 * drift between the two implementations shows up as a failing test on
	 * whichever side moved rather than as a preview that disagrees with the
	 * download.
	 */
	it("accepts a Verein's dark blue on white", () => {
		expect(hasEnoughQrContrast('#003366', '#ffffff')).toBe(true);
	});

	it('refuses yellow on white', () => {
		expect(hasEnoughQrContrast('#ffd700', '#ffffff')).toBe(false);
	});

	it('refuses anything that is not a six-digit hex colour', () => {
		expect(hasEnoughQrContrast('#fff', '#ffffff')).toBe(false);
		expect(hasEnoughQrContrast('rebeccapurple', '#ffffff')).toBe(false);
	});

	it('exposes the threshold it enforces', () => {
		expect(MIN_QR_CONTRAST_RATIO).toBe(4.5);
	});
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter @kurze-url/web test -- qr-contrast` Expected: FAIL — `Failed to resolve import "./qr-contrast"`.

- [ ] **Step 3: Write the mirror**

Create `apps/web/src/lib/qr-contrast.ts`:

```ts
/**
 * A browser-side copy of the contrast rule that
 * `apps/api/internal/qr/contrast.go` enforces, so a colour pair a camera
 * cannot read is refused before a request goes out.
 *
 * Unlike `link-password.ts`, this mirror is not expected to drift: the
 * password policy mirrors a curated word list, and this mirrors a closed
 * formula out of WCAG 2.1. There is nothing here to curate. The API remains
 * the enforcement point either way — a 422 still renders under the control.
 */

/** WCAG's floor for normal text, not its looser 3:1 for graphics. A QR module is smaller than a glyph and is read by a phone camera, not an eye. */
export const MIN_QR_CONTRAST_RATIO = 4.5;

const HEX_COLOR = /^#?[0-9a-f]{6}$/i;

/** Returns the three sRGB channels as 0–255, or `null` for anything that is not `rrggbb`. */
function channels(raw: string): [number, number, number] | null {
	if (!HEX_COLOR.test(raw)) return null;
	const digits = raw.startsWith('#') ? raw.slice(1) : raw;
	return [
		Number.parseInt(digits.slice(0, 2), 16),
		Number.parseInt(digits.slice(2, 4), 16),
		Number.parseInt(digits.slice(4, 6), 16),
	];
}

/** WCAG 2.1's relative luminance: sRGB channels linearised, then weighted. */
function relativeLuminance([r, g, b]: [number, number, number]): number {
	const channel = (value: number): number => {
		const s = value / 255;
		return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
	};
	return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

/**
 * `(lighter + 0.05) / (darker + 0.05)`, so the argument order does not
 * matter. Returns `1` — the worst possible ratio — for an unparseable colour,
 * so a malformed value fails closed rather than passing the check by
 * accident.
 */
export function qrContrastRatio(a: string, b: string): number {
	const first = channels(a);
	const second = channels(b);
	if (!first || !second) return 1;

	const la = relativeLuminance(first);
	const lb = relativeLuminance(second);
	const lighter = Math.max(la, lb);
	const darker = Math.min(la, lb);
	return (lighter + 0.05) / (darker + 0.05);
}

export function hasEnoughQrContrast(foreground: string, background: string): boolean {
	return qrContrastRatio(foreground, background) >= MIN_QR_CONTRAST_RATIO;
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `pnpm --filter @kurze-url/web test -- qr-contrast` Expected: PASS.

- [ ] **Step 5: Write the failing restyler test**

Create `apps/web/src/lib/qr-svg.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { qrSvgDataUrl, restyleQrSvg } from './qr-svg';

/** The exact shape `apps/api/internal/qr` emits: one background `<rect>`, one `<path>` of per-module subpaths, a viewBox and no width/height. */
const svg = [
	'<svg xmlns="http://www.w3.org/2000/svg" version="1.1" viewBox="0 0 37 37" stroke="none">',
	'\t<rect width="37" height="37" fill="#FFFFFF"/>',
	'\t<path d="M4,4h1v1h-1z M6,4h1v1h-1z" fill="#000000"/>',
	'</svg>',
	'',
].join('\n');

describe('restyleQrSvg', () => {
	/**
	 * The property the whole preview design rests on: neither colour changes
	 * the QR matrix, so the browser can recolour a document the API produced
	 * instead of generating a second one. A generator in TypeScript would
	 * drift from the Go one, and drift in an image means the preview shows
	 * something the download does not deliver.
	 */
	it('recolours without touching the path data', () => {
		const restyled = restyleQrSvg(svg, { background: '#fffff5', foreground: '#003366' });

		expect(restyled).toContain('#003366');
		expect(restyled).toContain('#fffff5');
		expect(restyled).toContain('M4,4h1v1h-1z M6,4h1v1h-1z');
		expect(restyled).not.toContain('#000000');
		expect(restyled).not.toContain('#FFFFFF');
	});

	it('keeps the viewBox, so the code still scales to whatever box it is put in', () => {
		expect(restyleQrSvg(svg, { background: '#ffffff', foreground: '#000000' })).toContain(
			'viewBox="0 0 37 37"',
		);
	});

	it('returns the document unchanged when it is not parseable as SVG', () => {
		expect(restyleQrSvg('not an svg', { background: '#ffffff', foreground: '#000000' })).toBe(
			'not an svg',
		);
	});
});

describe('qrSvgDataUrl', () => {
	/**
	 * A data URL in an `<img src>`, not `dangerouslySetInnerHTML`: the
	 * document comes from our own API, but an `<img>` cannot execute
	 * anything a future change might put in it, and it carries real `alt`
	 * text.
	 */
	it('produces an image data URL the browser can render', () => {
		const url = qrSvgDataUrl(svg);

		expect(url.startsWith('data:image/svg+xml,')).toBe(true);
		expect(decodeURIComponent(url.slice('data:image/svg+xml,'.length))).toContain('<svg');
	});
});
```

- [ ] **Step 6: Run it to verify it fails**

Run: `pnpm --filter @kurze-url/web test -- qr-svg` Expected: FAIL — `Failed to resolve import "./qr-svg"`.

- [ ] **Step 7: Write the restyler**

Create `apps/web/src/lib/qr-svg.ts`:

```ts
/**
 * Recolouring for the QR preview. This module does not generate QR codes and
 * must never start to: the browser restyles a document the API produced, and
 * that is the property the whole preview design rests on. A second generator
 * in TypeScript would drift from the Go one, and drift in an image means the
 * preview shows something the download does not deliver.
 *
 * Neither colour changes the matrix — colours are attributes, and the size is
 * a number in the viewport — so one fetch per link is enough for any number
 * of colour changes.
 */

interface QrColors {
	background: string;
	foreground: string;
}

/**
 * Sets the background `<rect>`'s and the module `<path>`'s `fill` attributes.
 *
 * Parsed with `DOMParser` rather than string-replaced: the fills are the only
 * two attributes that may change, and a regular expression over path data
 * that happens to contain a colour-shaped substring is exactly the kind of
 * silent corruption an image will not report. An unparseable document is
 * returned untouched, so a malformed response renders as a broken image
 * rather than as a plausible wrong one.
 */
export function restyleQrSvg(svg: string, { background, foreground }: QrColors): string {
	const parsed = new DOMParser().parseFromString(svg, 'image/svg+xml');
	if (parsed.querySelector('parsererror') || !parsed.documentElement) return svg;
	if (parsed.documentElement.nodeName !== 'svg') return svg;

	const rect = parsed.querySelector('rect');
	const path = parsed.querySelector('path');
	if (!rect || !path) return svg;

	rect.setAttribute('fill', background);
	path.setAttribute('fill', foreground);

	return new XMLSerializer().serializeToString(parsed.documentElement);
}

/**
 * An `<img src>` value. Percent-encoded rather than base64 so the payload
 * stays inspectable in devtools and needs no `btoa` round trip — the document
 * is ASCII, since go-qr emits no text nodes at all.
 */
export function qrSvgDataUrl(svg: string): string {
	return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}
```

- [ ] **Step 8: Run it to verify it passes**

Run: `pnpm --filter @kurze-url/web test -- qr-svg` Expected: PASS.

- [ ] **Step 9: Write the failing classifier test**

Append to `apps/web/src/lib/api-errors.test.ts`, inside the existing top-level `describe('classifyApiError', ...)` block (or as a new top-level `describe` if the file has no single wrapper — check the file's structure first):

```ts
/**
 * `getLinkQR` (apps/api/internal/api/link_qr.go) answers both of its
 * refusals with a typed detail on the query parameter at fault, the same
 * convention the password policy and `deleteDomain` already use. Reading
 * `location` rather than matching the message means a reworded message
 * cannot silently turn a precise reason into a generic failure.
 */
it('reads a QR contrast refusal off query.fg', () => {
	expect(
		classifyApiError({
			errors: [{ location: 'query.fg', message: 'too close', value: 'low_contrast' }],
			status: 422,
		}),
	).toEqual({ kind: 'qrRejected', reason: 'low_contrast' });
});

it('reads a QR size refusal off query.size', () => {
	expect(
		classifyApiError({
			errors: [{ location: 'query.size', message: 'PNG only', value: 'size_requires_png' }],
			status: 422,
		}),
	).toEqual({ kind: 'qrRejected', reason: 'size_requires_png' });
});

it('falls back to a generic QR rejection for a token this build does not know', () => {
	expect(
		classifyApiError({
			errors: [{ location: 'query.fg', message: 'nope', value: 'invented_by_a_newer_server' }],
			status: 422,
		}),
	).toEqual({ kind: 'qrRejected', reason: 'rejected' });
});

it('leaves a 422 on some other query parameter to the field path', () => {
	expect(
		classifyApiError({
			errors: [{ location: 'query.per_page', message: 'too large' }],
			status: 422,
		}),
	).toEqual({ fields: { per_page: 'too large' }, kind: 'fields' });
});
```

- [ ] **Step 10: Run it to verify it fails**

Run: `pnpm --filter @kurze-url/web test -- api-errors` Expected: FAIL — the QR cases classify as `fields`, not `qrRejected`.

- [ ] **Step 11: Extend the classifier**

In `apps/web/src/lib/api-errors.ts`:

Add the exported reason type immediately below the `import type { LinkPasswordReason } from './link-password';` line:

```ts
/**
 * The reason tokens `getLinkQR` (apps/api/internal/api/link_qr.go) can send
 * on a QR refusal. Each one is a Go sentinel's own `Error()` string, or —
 * for `size_requires_png` — a literal the handler owns; both are wire
 * contracts pinned by that package's tests, not prose.
 */
export type QrRejectionReason = 'invalid_color' | 'low_contrast' | 'size_requires_png';
```

Add `| { kind: 'qrRejected'; reason: QrRejectionReason | 'rejected' }` to the `ApiFailure` union, immediately after the `passwordRejected` member.

Add, immediately below `passwordRejectionOf`:

```ts
/**
 * Written as a `switch` rather than a `Set` for the same reason
 * `isKnownLinkPasswordReason` above is: TypeScript narrows a `string` to a
 * literal union across matching `case`s on its own, so this needs no type
 * assertion.
 */
function isKnownQrRejectionReason(value: string): value is QrRejectionReason {
	switch (value) {
		case 'invalid_color':
		case 'low_contrast':
		case 'size_requires_png':
			return true;
		default:
			return false;
	}
}

/**
 * `GET /v1/links/{link_id}/qr` is the only operation with `fg`, `bg` or
 * `size` query parameters, so matching on those three locations cannot
 * collide with another endpoint's 422. A 422 on any *other* query parameter
 * returns `undefined` and falls through to `fieldsOf`, so pagination and
 * filter errors keep the shape their own call sites already read.
 *
 * Returns `'rejected'`, not `undefined`, whenever the detail is on one of
 * those three but carries no value this build can use — a missing `value`, or
 * a token a newer server knows about and this build does not.
 */
const QR_LOCATIONS = new Set(['query.bg', 'query.fg', 'query.size']);

function qrRejectionOf(error: unknown): (QrRejectionReason | 'rejected') | undefined {
	for (const detail of problemDetailsOf(error)) {
		if (detail.location === undefined || !QR_LOCATIONS.has(detail.location)) continue;
		if (typeof detail.value === 'string' && isKnownQrRejectionReason(detail.value)) {
			return detail.value;
		}
		return 'rejected';
	}
	return undefined;
}
```

In `classifyApiError`, inside the `if (status === 400 || status === 422) {` block, immediately after the `passwordRejectionOf` branch, add:

```ts
const qrReason = qrRejectionOf(error);
if (qrReason !== undefined) return { kind: 'qrRejected', reason: qrReason };
```

- [ ] **Step 12: Run the lib tests**

Run: `pnpm --filter @kurze-url/web test -- api-errors qr-contrast qr-svg && pnpm typecheck && pnpm lint` Expected: all PASS, typecheck and lint clean.

- [ ] **Step 13: Commit**

```bash
pnpm format
but commit -b feat/link-qr -m "feat(web): mirror the qr contrast rule"
```

---

### Task 7: The server functions

**Files:**

- Modify: `apps/web/src/server/links.ts`
- Test: `apps/web/src/server/links.test.ts`

**Interfaces:**

- Consumes: `getLinkQr` from `@kurze-url/api-client` (Task 5).
- Produces:
  - `linkQrSvgFor(request: Request, linkId: string): Promise<string>` and `linkQrSvgFn`, taking `{ linkId }`.
  - `linkQrDownloadFor(request, linkId, options): Promise<QrDownload>` and `linkQrDownloadFn`, taking `{ background, foreground, format, linkId, size }`.
  - `interface QrDownload { base64: string; contentType: string }`.
  - `qrBodyBytes(body: unknown): Promise<Uint8Array>` (exported for its test).

**Read before writing:** the `setLinkPasswordFor` / `setLinkPasswordFn` pair at the end of `apps/web/src/server/links.ts` — the new pair follows exactly that shape, including `createServerOnlyFn`, `requireSession`, `flushSessionCookies` and `throwOnError: true`.

- [ ] **Step 1: Write the failing test**

Append to `apps/web/src/server/links.test.ts`:

```ts
describe('qrBodyBytes', () => {
	/**
	 * The generated client parses by `Content-Type` (`getParseAs` in
	 * `packages/api-client/src/generated/client/utils.gen.ts` maps anything
	 * starting with `image/` to `blob`), so a QR response arrives as a
	 * `Blob`. Narrowing at runtime rather than casting the generated type
	 * means a regenerated client that types the body differently changes
	 * nothing here.
	 */
	it('reads a Blob body', async () => {
		const bytes = await qrBodyBytes(new Blob([new Uint8Array([1, 2, 3])]));

		expect(Array.from(bytes)).toEqual([1, 2, 3]);
	});

	it('reads a string body', async () => {
		const bytes = await qrBodyBytes('<svg/>');

		expect(new TextDecoder().decode(bytes)).toBe('<svg/>');
	});

	it('refuses anything else rather than shipping an empty image', async () => {
		await expect(qrBodyBytes({ not: 'an image' })).rejects.toThrow(TypeError);
	});
});
```

Add `qrBodyBytes` to the file's existing import from `./links`.

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter @kurze-url/web test -- server/links` Expected: FAIL — `qrBodyBytes` is not exported.

- [ ] **Step 3: Write the implementation**

Append to `apps/web/src/server/links.ts`:

```ts
/** What `linkQrDownloadFor` hands back: the image, base64-encoded so it survives the server-function boundary, plus the media type to rebuild a `Blob` with. */
export interface QrDownload {
	base64: string;
	contentType: string;
}

/** The colours, format and size one download asks for. */
export interface QrDownloadOptions {
	/** `rrggbb`, no leading `#` — a raw `#` in a query string is the fragment delimiter and would never reach the API. */
	background: string;
	foreground: string;
	format: 'png' | 'svg';
	/** Pixels. Ignored for SVG, and deliberately not sent then: the API answers 422 for a size on an SVG request. */
	size: number;
}

/**
 * Narrows whatever the generated client produced for an image response into
 * bytes.
 *
 * The runtime behaviour is settled: `getParseAs` in the generated client maps
 * any `image/*` content type to `blob`, so this receives a `Blob`. The string
 * branch and the throw exist because the *declared* type is whatever
 * `@hey-api/openapi-ts` chose for a `{type: string, format: binary}` schema,
 * and a regenerated client is free to change that without changing the
 * runtime. Throwing beats defaulting: an empty image is a broken download
 * that reports success.
 */
export async function qrBodyBytes(body: unknown): Promise<Uint8Array> {
	if (body instanceof Blob) return new Uint8Array(await body.arrayBuffer());
	if (typeof body === 'string') return new TextEncoder().encode(body);
	throw new TypeError('unexpected QR response body');
}

/**
 * Fetches the link's QR code once, as SVG in the default colours.
 *
 * The card recolours this document locally for every preview change — neither
 * colour nor size changes the QR matrix, so re-requesting on every drag of a
 * colour picker would spend the endpoint's whole rate limit in seconds for no
 * new information. Two requests per link, not fifty.
 */
export const linkQrSvgFor = createServerOnlyFn(
	async (request: Request, linkId: string): Promise<string> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await getLinkQr({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			query: { format: 'svg' },
			throwOnError: true,
		});
		return new TextDecoder().decode(await qrBodyBytes(data));
	},
);

export const linkQrSvgFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string }) => data)
	.handler(async ({ data }) => linkQrSvgFor(getRequest(), data.linkId));

/**
 * The second and last request: the actual download, in the chosen format and
 * colours.
 *
 * `size` is sent only for PNG. A vector has no pixel size, and the API
 * answers 422 rather than ignoring the parameter — the frontend hiding the
 * control is the other half of that, not a replacement for it.
 *
 * The bytes come back base64-encoded because a server function's return value
 * is serialised, and a `Uint8Array` does not survive that intact.
 */
export const linkQrDownloadFor = createServerOnlyFn(
	async (request: Request, linkId: string, options: QrDownloadOptions): Promise<QrDownload> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await getLinkQr({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			query: {
				bg: options.background,
				fg: options.foreground,
				format: options.format,
				...(options.format === 'png' ? { size: options.size } : {}),
			},
			throwOnError: true,
		});

		const bytes = await qrBodyBytes(data);
		return {
			base64: Buffer.from(bytes).toString('base64'),
			contentType: options.format === 'png' ? 'image/png' : 'image/svg+xml',
		};
	},
);

export const linkQrDownloadFn = createServerFn({ method: 'POST' })
	.validator((data: QrDownloadOptions & { linkId: string }) => data)
	.handler(async ({ data }) =>
		linkQrDownloadFor(getRequest(), data.linkId, {
			background: data.background,
			foreground: data.foreground,
			format: data.format,
			size: data.size,
		}),
	);
```

Add `getLinkQr` to the existing `@kurze-url/api-client` import at the top of the file, keeping the import list alphabetical.

- [ ] **Step 4: Run the tests**

Run: `pnpm --filter @kurze-url/web test -- server/links && pnpm typecheck && pnpm lint` Expected: PASS, clean.

If `pnpm typecheck` rejects the `query` object because the generated `GetLinkQrData['query']` types `size` as required or the colours differently, adapt the object to the generated type — do not cast it away, and record the difference in the task report.

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/link-qr -m "feat(web): add the qr server functions"
```

---

### Task 8: The QR card

**Files:**

- Create: `apps/web/src/components/link-qr-card.tsx`
- Test: `apps/web/src/components/link-qr-card.test.tsx`
- Create: `apps/web/src/components/link-qr-card.stories.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `hasEnoughQrContrast` (Task 6), `restyleQrSvg`, `qrSvgDataUrl` (Task 6), `QrRejectionReason` (Task 6), `Button` from `./ui/button`.
- Produces: `LinkQRCard` and `LinkQRCardProps`:

```ts
export interface LinkQRCardProps {
	readonly isLoading: boolean;
	readonly onDismissRejection?: () => void;
	readonly onDownload: (options: {
		background: string;
		foreground: string;
		format: 'png' | 'svg';
		size: number;
	}) => Promise<void>;
	readonly rejection?: QrRejectionReason | 'rejected';
	readonly svg: string | undefined;
}
```

- [ ] **Step 1: Add the translation keys**

In `apps/web/src/i18n/locales/en.json`, inside the `links` object, immediately after `"passwordRejected"` (add a comma to that line), add:

```json
		"qrHeading": "QR code",
		"qrExplainer": "The code points at the short URL, so you can change where the link goes without reprinting anything. Scans are counted separately from ordinary clicks.",
		"qrFormat": "File format",
		"qrFormatSvg": "SVG — scales to any size, best for print",
		"qrFormatPng": "PNG — a fixed-size image, best for screens",
		"qrForeground": "Code colour",
		"qrBackground": "Background colour",
		"qrSize": "Size in pixels",
		"qrSizeHint": "Applies to the PNG. The result is the largest whole size that fits, so it can come out slightly smaller.",
		"qrPreviewAlt": "Preview of this link's QR code",
		"qrPreviewLoading": "Loading the preview…",
		"qrPreviewUnavailable": "The preview could not be loaded.",
		"qrDownload": "Download",
		"qrLowContrast": "These two colours are too close together. A camera will not read the code — pick a darker code colour or a lighter background.",
		"qrInvalidColor": "Give the colours as six hex digits, for example 003366.",
		"qrSizeRequiresPng": "A size only applies to the PNG. Switch the format to PNG, or leave the size out.",
		"qrRejected": "That combination cannot be used. Change the colours or the size."
```

In `apps/web/src/i18n/locales/de.json`, in the same place:

```json
		"qrHeading": "QR-Code",
		"qrExplainer": "Der Code zeigt auf die Kurz-URL. Du kannst das Ziel des Links also ändern, ohne etwas neu zu drucken. Scans werden getrennt von normalen Klicks gezählt.",
		"qrFormat": "Dateiformat",
		"qrFormatSvg": "SVG — beliebig skalierbar, am besten für den Druck",
		"qrFormatPng": "PNG — Bild in fester Größe, am besten für Bildschirme",
		"qrForeground": "Farbe des Codes",
		"qrBackground": "Hintergrundfarbe",
		"qrSize": "Größe in Pixeln",
		"qrSizeHint": "Gilt für das PNG. Ausgegeben wird die größte ganze Größe, die hineinpasst — das Ergebnis kann also etwas kleiner ausfallen.",
		"qrPreviewAlt": "Vorschau des QR-Codes dieses Links",
		"qrPreviewLoading": "Vorschau wird geladen …",
		"qrPreviewUnavailable": "Die Vorschau konnte nicht geladen werden.",
		"qrDownload": "Herunterladen",
		"qrLowContrast": "Diese beiden Farben liegen zu dicht beieinander. Eine Kamera liest den Code so nicht — wähle eine dunklere Codefarbe oder einen helleren Hintergrund.",
		"qrInvalidColor": "Gib die Farben als sechs Hexadezimalstellen an, zum Beispiel 003366.",
		"qrSizeRequiresPng": "Eine Größe gilt nur für das PNG. Stelle das Format auf PNG um oder lass die Größe weg.",
		"qrRejected": "Diese Kombination ist nicht möglich. Ändere die Farben oder die Größe."
```

- [ ] **Step 2: Write the failing test**

Create `apps/web/src/components/link-qr-card.test.tsx`:

```tsx
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { LinkQRCard, type LinkQRCardProps } from './link-qr-card';

/** The exact shape `apps/api/internal/qr` emits — see `qr-svg.test.ts`, which uses the same fixture. */
const svg = [
	'<svg xmlns="http://www.w3.org/2000/svg" version="1.1" viewBox="0 0 37 37" stroke="none">',
	'\t<rect width="37" height="37" fill="#FFFFFF"/>',
	'\t<path d="M4,4h1v1h-1z" fill="#000000"/>',
	'</svg>',
].join('\n');

/** Same pattern as `link-password-card.test.tsx`'s `renderCard`: `useTranslation` needs an `I18nextProvider` in the tree. */
function renderCard(props: Partial<LinkQRCardProps> = {}): ReturnType<typeof render> {
	const merged: LinkQRCardProps = {
		isLoading: false,
		onDownload: vi.fn().mockResolvedValue(undefined),
		svg,
		...props,
	};
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<LinkQRCard {...merged} />
		</I18nextProvider>,
	);
}

describe('LinkQRCard', () => {
	it('shows the preview once the document has arrived', () => {
		renderCard();

		const preview = screen.getByRole('img', { name: "Preview of this link's QR code" });
		expect(preview.getAttribute('src')).toContain('data:image/svg+xml,');
	});

	it('says so while the document is still on its way', () => {
		renderCard({ isLoading: true, svg: undefined });

		expect(screen.getByText('Loading the preview…')).toBeInTheDocument();
	});

	it('says so when the document never arrived', () => {
		renderCard({ isLoading: false, svg: undefined });

		expect(screen.getByText('The preview could not be loaded.')).toBeInTheDocument();
	});

	/**
	 * The control is hidden rather than disabled: a vector has no pixel size,
	 * so there is nothing for it to mean. The API answers 422 for the same
	 * combination — this is the other half of that, not a replacement for it.
	 */
	it('hides the size control while SVG is selected and shows it for PNG', async () => {
		renderCard();

		expect(screen.queryByLabelText('Size in pixels')).not.toBeInTheDocument();

		await userEvent.selectOptions(screen.getByLabelText('File format'), 'png');

		expect(screen.getByLabelText('Size in pixels')).toBeInTheDocument();
	});

	/**
	 * The point of fetching once: recolouring is local, so changing a colour
	 * must not ask the parent for anything. `onDownload` is the only call
	 * this component ever makes, and it happens on the download control
	 * alone.
	 */
	it('recolours the preview without asking for a new document', async () => {
		const onDownload = vi.fn().mockResolvedValue(undefined);
		renderCard({ onDownload });

		const before = screen.getByRole('img').getAttribute('src');
		// `fireEvent.change`, not `userEvent.type`: `<input type="color">` is
		// not an editable text field, so `userEvent.clear` throws on it and
		// typing into it does nothing. The change event is what a real colour
		// picker dispatches anyway.
		fireEvent.change(screen.getByLabelText('Code colour'), { target: { value: '#003366' } });

		const after = screen.getByRole('img').getAttribute('src');
		expect(after).not.toBe(before);
		expect(decodeURIComponent(after ?? '')).toContain('#003366');
		expect(onDownload).not.toHaveBeenCalled();
	});

	it('refuses a low-contrast pair before it asks for a download', async () => {
		const onDownload = vi.fn().mockResolvedValue(undefined);
		renderCard({ onDownload });

		fireEvent.change(screen.getByLabelText('Code colour'), { target: { value: '#ffd700' } });
		await userEvent.click(screen.getByRole('button', { name: 'Download' }));

		expect(screen.getByRole('alert')).toHaveTextContent(/too close together/i);
		expect(onDownload).not.toHaveBeenCalled();
	});

	it('asks for the download with the chosen format, size and colours', async () => {
		const onDownload = vi.fn().mockResolvedValue(undefined);
		renderCard({ onDownload });

		await userEvent.selectOptions(screen.getByLabelText('File format'), 'png');
		const size = screen.getByLabelText('Size in pixels');
		await userEvent.clear(size);
		await userEvent.type(size, '1024');
		await userEvent.click(screen.getByRole('button', { name: 'Download' }));

		expect(onDownload).toHaveBeenCalledWith({
			background: 'ffffff',
			foreground: '000000',
			format: 'png',
			size: 1024,
		});
	});

	/** A reason the mirror did not predict still has to reach the reader — the same escape hatch `LinkPasswordCard`'s `rejection` prop is. */
	it('renders a rejection the API reported', () => {
		renderCard({ rejection: 'size_requires_png' });

		expect(screen.getByRole('alert')).toHaveTextContent(/only applies to the PNG/i);
	});

	it('renders an unrecognised rejection through the generic message', () => {
		renderCard({ rejection: 'rejected' });

		expect(screen.getByRole('alert')).toHaveTextContent(/cannot be used/i);
	});
});
```

- [ ] **Step 3: Run it to verify it fails**

Run: `pnpm --filter @kurze-url/web test -- link-qr-card` Expected: FAIL — `Failed to resolve import "./link-qr-card"`.

- [ ] **Step 4: Write the component**

Create `apps/web/src/components/link-qr-card.tsx`:

```tsx
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { QrRejectionReason } from '../lib/api-errors';
import { hasEnoughQrContrast } from '../lib/qr-contrast';
import { qrSvgDataUrl, restyleQrSvg } from '../lib/qr-svg';
import { Button } from './ui/button';

export interface LinkQRCardProps {
	/** True while the one SVG fetch is in flight. `svg` undefined with this false means the fetch failed. */
	readonly isLoading: boolean;
	/** Called when the reader changes a control, so a stale API-reported `rejection` does not linger over a combination they are already correcting. */
	readonly onDismissRejection?: () => void;
	/** Resolves when the download has been handed to the browser, rejects on failure. Colours are sent as bare `rrggbb`. */
	readonly onDownload: (options: {
		background: string;
		foreground: string;
		format: QrFormat;
		size: number;
	}) => Promise<void>;
	/** A reason the API returned that the mirrored contrast rule did not predict. */
	readonly rejection?: QrRejectionReason | 'rejected';
	/** The document fetched once for this link, in the default colours. */
	readonly svg: string | undefined;
}

/** Exported because `LinkQRCardProps` names it: an unexported type in a public prop makes the prop unnameable from a parent. */
export type QrFormat = 'png' | 'svg';

/**
 * Every reason the mirrored rule or the API's typed 422 can carry, mapped to
 * its translation key as a `Record` rather than a lookup function — adding a
 * reason to `QrRejectionReason` without adding it here is a compile error,
 * not a blank message a reader has no way to act on. Same shape as
 * `link-password-card.tsx`'s `messageKeys`.
 */
const messageKeys: Record<QrRejectionReason | 'rejected', string> = {
	invalid_color: 'links.qrInvalidColor',
	low_contrast: 'links.qrLowContrast',
	rejected: 'links.qrRejected',
	size_requires_png: 'links.qrSizeRequiresPng',
};

/**
 * The API's own defaults and bounds (`apps/api/internal/qr/qr.go`), repeated
 * here so the controls start where the endpoint would and refuse what it
 * would refuse. A drift costs one rejected request with a message under the
 * control, not a wrong image — the endpoint validates these regardless.
 */
const DEFAULT_FOREGROUND = '#000000';
const DEFAULT_BACKGROUND = '#ffffff';
const DEFAULT_SIZE = 512;
const MIN_SIZE = 64;
const MAX_SIZE = 2048;

/** The preview's own box, in CSS pixels. Small sizes render smaller so the control's effect is visible; large ones stop here rather than filling the page. */
const MAX_PREVIEW_PIXELS = 240;

/** The API takes `rrggbb`: a raw `#` in a query string is the fragment delimiter and never reaches the server. `<input type="color">` produces the `#` form, so it is stripped on the way out. */
function bare(color: string): string {
	return color.startsWith('#') ? color.slice(1) : color;
}

/**
 * The card that turns a link into something a Verein can print.
 *
 * It fetches nothing and generates nothing. The parent hands it one SVG
 * document the API produced; every colour change recolours that document in
 * place, because neither colour nor size changes the QR matrix. The API is
 * called again only for the download, through `onDownload`.
 *
 * That is the property worth protecting: a second QR generator in TypeScript
 * would drift from the Go one, and drift in an image means the preview shows
 * something the download does not deliver.
 */
export function LinkQRCard({
	isLoading,
	onDismissRejection,
	onDownload,
	rejection,
	svg,
}: LinkQRCardProps): React.JSX.Element {
	const { t } = useTranslation();
	const formatId = useId();
	const foregroundId = useId();
	const backgroundId = useId();
	const sizeId = useId();
	const sizeHintId = useId();
	const errorId = useId();

	const [format, setFormat] = useState<QrFormat>('svg');
	const [foreground, setForeground] = useState(DEFAULT_FOREGROUND);
	const [background, setBackground] = useState(DEFAULT_BACKGROUND);
	const [size, setSize] = useState(DEFAULT_SIZE);
	// The reason the *local* check found, distinct from `rejection` (the
	// API's own finding): changing a control clears this and asks the parent
	// to clear its half, so a stale message does not linger over a
	// combination the reader has already corrected.
	const [localReason, setLocalReason] = useState<QrRejectionReason | null>(null);

	const reason = localReason ?? rejection;
	const message = reason ? t(messageKeys[reason]) : undefined;

	function changed(): void {
		setLocalReason(null);
		onDismissRejection?.();
	}

	async function handleDownload(): Promise<void> {
		if (!hasEnoughQrContrast(foreground, background)) {
			setLocalReason('low_contrast');
			return;
		}
		setLocalReason(null);
		try {
			await onDownload({
				background: bare(background),
				foreground: bare(foreground),
				format,
				size,
			});
		} catch {
			// The parent classifies why and feeds a reason back through
			// `rejection`, or renders its own banner for a failure that is not
			// a QR refusal at all. Nothing to do here.
		}
	}

	const previewSize = format === 'png' ? Math.min(size, MAX_PREVIEW_PIXELS) : MAX_PREVIEW_PIXELS;
	const preview = svg ? restyleQrSvg(svg, { background, foreground }) : undefined;

	return (
		<section>
			<h2>{t('links.qrHeading')}</h2>
			<p>{t('links.qrExplainer')}</p>

			{preview ? (
				<img
					alt={t('links.qrPreviewAlt')}
					height={previewSize}
					src={qrSvgDataUrl(preview)}
					width={previewSize}
				/>
			) : (
				<p>{t(isLoading ? 'links.qrPreviewLoading' : 'links.qrPreviewUnavailable')}</p>
			)}

			<div>
				<label htmlFor={formatId}>{t('links.qrFormat')}</label>
				<select
					id={formatId}
					onChange={(event) => {
						setFormat(event.target.value === 'png' ? 'png' : 'svg');
						changed();
					}}
					value={format}
				>
					<option value="svg">{t('links.qrFormatSvg')}</option>
					<option value="png">{t('links.qrFormatPng')}</option>
				</select>
			</div>

			<div>
				<label htmlFor={foregroundId}>{t('links.qrForeground')}</label>
				<input
					id={foregroundId}
					onChange={(event) => {
						setForeground(event.target.value);
						changed();
					}}
					type="color"
					value={foreground}
				/>
			</div>

			<div>
				<label htmlFor={backgroundId}>{t('links.qrBackground')}</label>
				<input
					id={backgroundId}
					onChange={(event) => {
						setBackground(event.target.value);
						changed();
					}}
					type="color"
					value={background}
				/>
			</div>

			{format === 'png' ? (
				<div>
					<label htmlFor={sizeId}>{t('links.qrSize')}</label>
					<input
						aria-describedby={sizeHintId}
						id={sizeId}
						max={MAX_SIZE}
						min={MIN_SIZE}
						onChange={(event) => {
							setSize(Number(event.target.value));
							changed();
						}}
						type="number"
						value={size}
					/>
					<p id={sizeHintId}>{t('links.qrSizeHint')}</p>
				</div>
			) : null}

			{message ? (
				<p id={errorId} role="alert">
					{message}
				</p>
			) : null}

			<Button
				aria-describedby={message ? errorId : undefined}
				onClick={() => {
					void handleDownload();
				}}
				type="button"
			>
				{t('links.qrDownload')}
			</Button>
		</section>
	);
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `pnpm --filter @kurze-url/web test -- link-qr-card` Expected: PASS.

If jsdom normalises `<input type="color">` values differently from what the assertions expect (it lower-cases them, which these fixtures already are), adjust the expected string rather than the component, and say so in the task report.

- [ ] **Step 6: Write the stories**

Create `apps/web/src/components/link-qr-card.stories.tsx`:

```tsx
import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { LinkQRCard } from './link-qr-card';

/** Mirrors `link-qr-card.test.tsx`'s own fixture. */
const svg = [
	'<svg xmlns="http://www.w3.org/2000/svg" version="1.1" viewBox="0 0 37 37" stroke="none">',
	'\t<rect width="37" height="37" fill="#FFFFFF"/>',
	'\t<path d="M4,4h1v1h-1z M6,4h1v1h-1z M8,4h1v1h-1z" fill="#000000"/>',
	'</svg>',
].join('\n');

const meta = {
	component: LinkQRCard,
	title: 'Links/LinkQRCard',
} satisfies Meta<typeof LinkQRCard>;

export default meta;

/** The ordinary state: the document has arrived and the preview is live. */
export const Ready: StoryObj<typeof meta> = {
	args: { isLoading: false, onDownload: fn(), svg },
};

/** The one fetch is still in flight. */
export const Loading: StoryObj<typeof meta> = {
	args: { isLoading: true, onDownload: fn(), svg: undefined },
};

/** The fetch failed. The controls stay usable — a download can still succeed where a preview did not. */
export const PreviewUnavailable: StoryObj<typeof meta> = {
	args: { isLoading: false, onDownload: fn(), svg: undefined },
};

/**
 * A refusal the API reported. This is what puts the error-association wiring
 * (`role="alert"`, `aria-describedby`) in front of the a11y addon, the same
 * reasoning `link-password-card.stories.tsx`'s `WithRejection` gives.
 */
export const WithRejection: StoryObj<typeof meta> = {
	args: { isLoading: false, onDownload: fn(), rejection: 'low_contrast', svg },
};
```

- [ ] **Step 7: Run the story tests, typecheck and lint**

Run: `pnpm --filter @kurze-url/web test:storybook -- link-qr-card && pnpm typecheck && pnpm lint` Expected: PASS, clean — including the a11y addon's checks on all four stories.

- [ ] **Step 8: Commit**

```bash
pnpm format
but commit -b feat/link-qr -m "feat(web): add the link qr card"
```

---

### Task 9: Mount the card on the link detail route

**Files:**

- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`
- Test: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.test.ts`

**Interfaces:**

- Consumes: `LinkQRCard` (Task 8); `linkQrSvgFn`, `linkQrDownloadFn`, `QrDownloadOptions` (Task 7); `classifyApiError`, `QrRejectionReason` (Task 6).
- Produces: `handleQrError(error: unknown, handlers: QrErrorHandlers): void` and `saveQrDownload(download: { base64: string; contentType: string }, filename: string, doc: Document): void`, both exported so the route's test can drive them without a router.

**Read before writing:** the whole route file. The new wiring follows `handlePasswordError` exactly: an `unauthenticated` classification navigates to login, a `qrRejected` classification goes to the card's own `rejection` prop, and everything else falls through to the one page banner the route already has.

- [ ] **Step 1: Write the failing test**

Append to `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.test.ts`:

```ts
describe('handleQrError', () => {
	it('sends an expired session to login', () => {
		const handlers = {
			navigateToLogin: vi.fn(),
			setFailure: vi.fn(),
			setQrRejection: vi.fn(),
		};

		handleQrError({ status: 401 }, handlers);

		expect(handlers.navigateToLogin).toHaveBeenCalledOnce();
		expect(handlers.setFailure).not.toHaveBeenCalled();
	});

	/**
	 * A QR refusal belongs beside the controls that caused it, never in the
	 * page banner — the card already renders it under the colour picker. Same
	 * split `handlePasswordError` makes for a policy rejection.
	 */
	it('routes a QR refusal to the card, not the banner', () => {
		const handlers = {
			navigateToLogin: vi.fn(),
			setFailure: vi.fn(),
			setQrRejection: vi.fn(),
		};

		handleQrError(
			{ errors: [{ location: 'query.fg', message: 'x', value: 'low_contrast' }], status: 422 },
			handlers,
		);

		expect(handlers.setQrRejection).toHaveBeenCalledWith('low_contrast');
		expect(handlers.setFailure).toHaveBeenCalledWith(null);
	});

	it('routes everything else to the page banner', () => {
		const handlers = {
			navigateToLogin: vi.fn(),
			setFailure: vi.fn(),
			setQrRejection: vi.fn(),
		};

		handleQrError({ status: 429 }, handlers);

		expect(handlers.setQrRejection).toHaveBeenCalledWith(undefined);
		expect(handlers.setFailure).toHaveBeenCalledWith({ kind: 'rateLimited' });
	});
});

describe('saveQrDownload', () => {
	/**
	 * The bytes cross the server-function boundary base64-encoded, so the
	 * browser has to rebuild them before it can hand the file to the reader.
	 * Driving it through an injected `Document` is what lets this run without
	 * a router or a real click.
	 */
	it('hands the decoded bytes to the browser under the link’s own name', () => {
		const anchor = { click: vi.fn(), download: '', href: '', rel: '' };
		const doc = {
			body: { appendChild: vi.fn(), removeChild: vi.fn() },
			createElement: vi.fn().mockReturnValue(anchor),
		} as unknown as Document;
		const createObjectURL = vi.fn().mockReturnValue('blob:fake');
		const revokeObjectURL = vi.fn();
		// `saveQrDownload` uses nothing else off `URL`, so a two-method stand-in
		// is the whole surface it needs.
		vi.stubGlobal('URL', { createObjectURL, revokeObjectURL });

		saveQrDownload({ base64: btoa('<svg/>'), contentType: 'image/svg+xml' }, 'sommerfest.svg', doc);

		expect(anchor.download).toBe('sommerfest.svg');
		expect(anchor.href).toBe('blob:fake');
		expect(anchor.click).toHaveBeenCalledOnce();
		expect(revokeObjectURL).toHaveBeenCalledWith('blob:fake');

		vi.unstubAllGlobals();
	});
});
```

Add `handleQrError` and `saveQrDownload` to the file's existing import from the route module.

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm --filter @kurze-url/web test -- links.\$linkId` Expected: FAIL — neither export exists.

- [ ] **Step 3: Add the two exported helpers**

In `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`, immediately after `applyPasswordSuccess`, add:

```tsx
/** Dependencies `handleQrError` needs from the component — see `PasswordErrorHandlers` above for why the dependencies are a parameter rather than a closure. */
interface QrErrorHandlers {
	navigateToLogin: () => void;
	setFailure: (failure: ApiFailure | null) => void;
	setQrRejection: (reason: QrRejectionReason | 'rejected' | undefined) => void;
}

/**
 * The QR counterpart of `handlePasswordError`, and the same split: a refusal
 * the endpoint keyed to one of its own query parameters belongs under the
 * control that caused it, and everything else — a rate limit, a 404, a
 * genuine 500 — falls through to the one banner this route already has.
 */
export function handleQrError(error: unknown, handlers: QrErrorHandlers): void {
	const classified = classifyApiError(error);
	if (classified.kind === 'unauthenticated') {
		handlers.navigateToLogin();
		return;
	}
	if (classified.kind === 'qrRejected') {
		handlers.setFailure(null);
		handlers.setQrRejection(classified.reason);
		return;
	}
	handlers.setQrRejection(undefined);
	handlers.setFailure(classified);
}

/**
 * Turns the base64 a QR download arrives as back into a file the browser
 * saves.
 *
 * The bytes are encoded because a server function's return value is
 * serialised and a `Uint8Array` does not survive that (see
 * `linkQrDownloadFor` in `server/links.ts`). `doc` is a parameter rather than
 * the global so the route's test can drive this with a stub instead of a real
 * click, the same reasoning every other exported helper in this file follows.
 *
 * The object URL is revoked immediately: the click has already started the
 * save, and leaving it alive would pin the whole image in memory for the life
 * of the document.
 */
export function saveQrDownload(
	download: { base64: string; contentType: string },
	filename: string,
	doc: Document,
): void {
	const binary = atob(download.base64);
	const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
	const url = URL.createObjectURL(new Blob([bytes], { type: download.contentType }));

	const anchor = doc.createElement('a');
	anchor.download = filename;
	anchor.href = url;
	anchor.rel = 'noopener';
	doc.body.appendChild(anchor);
	anchor.click();
	doc.body.removeChild(anchor);
	URL.revokeObjectURL(url);
}
```

Add `useQuery` to the `@tanstack/react-query` import, `QrRejectionReason` to the `../../lib/api-errors` type import, `LinkQRCard` to the component imports, and `linkQrDownloadFn`, `linkQrSvgFn` to the `../../server/links` import — keeping each list alphabetical.

- [ ] **Step 4: Wire the card into the component**

In `RouteComponent`, immediately after the `passwordRejection` state declaration, add:

```tsx
const [qrRejection, setQrRejection] = useState<QrRejectionReason | 'rejected' | undefined>(
	undefined,
);

// One fetch per link, for the whole life of the card. Every colour and
// size change is a local restyle of this document — see `LinkQRCard`.
// `staleTime: Infinity` says that out loud: the matrix depends only on the
// slug and the hostname, neither of which changes without a navigation
// that remounts this route.
const qrQuery = useQuery({
	queryFn: () => linkQrSvgFn({ data: { linkId } }),
	queryKey: ['link-qr', linkId],
	staleTime: Number.POSITIVE_INFINITY,
});

const qrDownloadMutation = useMutation({
	mutationFn: (options: {
		background: string;
		foreground: string;
		format: 'png' | 'svg';
		size: number;
	}) => linkQrDownloadFn({ data: { ...options, linkId } }),
	onError: (error: unknown) => {
		handleQrError(error, {
			navigateToLogin: () => {
				void router.navigate({ to: '/login' });
			},
			setFailure,
			setQrRejection,
		});
	},
});
```

Then, in the returned JSX, immediately after `<LinkPasswordCard … />` and before `<ConfirmDelete … />`, add:

```tsx
<LinkQRCard
	isLoading={qrQuery.isPending}
	key={linkId}
	onDismissRejection={() => {
		setQrRejection(undefined);
	}}
	onDownload={async (options) => {
		const download = await qrDownloadMutation.mutateAsync(options);
		setQrRejection(undefined);
		setFailure(null);
		saveQrDownload(download, `${link.slug}.${options.format}`, document);
	}}
	rejection={qrRejection}
	svg={qrQuery.data}
/>
```

- [ ] **Step 5: Run the tests**

Run: `pnpm --filter @kurze-url/web test && pnpm typecheck && pnpm lint` Expected: the whole web suite PASSes, typecheck and lint clean.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-qr -m "feat(web): show the qr card on a link"
```

---

### Task 10: The end-to-end path and the documentation

**Files:**

- Modify: `apps/web/e2e/links.spec.ts`
- Modify: `CLAUDE.md`
- Modify: `docs/planning/06-api-design.md`

**Interfaces:**

- Consumes: everything above.
- Produces: nothing further tasks depend on.

- [ ] **Step 1: Add the e2e spec**

In `apps/web/e2e/links.spec.ts`, immediately after the `protects a link with a password and removes it again` test, add:

```ts
/**
 * The dashboard side only. The image itself is deliberately not asserted
 * here — that is what `apps/api/internal/qr`'s decode test is for, which
 * reads the rendered code back and compares the decoded string. What this
 * covers is the wiring nothing else does: the preview reaching the page at
 * all, and the download control producing a file.
 */
test('downloads a link’s QR code', async ({ page, teamSlug }) => {
	await createLink(page, teamSlug, `https://example.org/qr-${Date.now()}`);

	await page.getByRole('link', { name: /edit/i }).click();

	const preview = page.getByRole('img', { name: /preview of this link/i });
	await expect(preview).toBeVisible();

	const download = page.waitForEvent('download');
	await page.getByRole('button', { name: /^download$/i }).click();

	expect((await download).suggestedFilename()).toMatch(/\.svg$/);
});
```

- [ ] **Step 2: Update the API-surface summary in `CLAUDE.md`**

On line 80, replace:

```
`GET /links/{id}/qr` (returns raw image bytes)
```

with:

```
`GET /links/{id}/qr` (returns raw image bytes; `format` svg|png, `size` 64–2048 PNG-only, `fg`/`bg` as `rrggbb`)
```

- [ ] **Step 3: Replace the two QR lines under "Non-obvious constraints"**

Replace lines 112–113 of `CLAUDE.md`:

```
- **QR codes always encode the short URL**, never the destination — that's what makes changing a destination safe.
- `piglig/go-qr` is the QR library; validate the centered-logo size against the chosen error-correction budget.
```

with:

```
- **QR codes always encode the short URL**, never the destination — that's what makes changing a destination safe. The encoded string is `<scheme>://<hostname>/<slug>?qr=1`, and the marker is the only thing in the system that can populate `link_click_stats`'s `qr_vs_regular` dimension: every row written before `GET /v1/links/{link_id}/qr` shipped says `regular`, because nothing could produce the other value. Changing a link's **slug** silently invalidates every code already printed; `PATCH /v1/links/{link_id}` allows that and warns about nothing, which was harmless only while no codes existed.
- **The browser never generates a QR code.** `apps/web`'s card fetches the SVG once per link and restyles that document locally — colours are attributes, size is a number in the viewport, and neither changes the matrix — so the API is called a second time only for the download. The request count is the smaller reason; the real one is that a second generator in TypeScript would drift from the Go one, and drift in an image means the preview shows something the download does not deliver. The **contrast rule** is mirrored (`apps/web/src/lib/qr-contrast.ts`), and that is safe where the password policy's mirror is not, because it is a closed WCAG formula rather than a curated corpus.
- `piglig/go-qr` is the QR library, and its two renderers do not measure the border the same way: PNG is `(modules + 2*border) * scale` pixels, SVG is `modules*scale + 2*border` user units. `internal/qr` therefore renders SVG at scale 1 — any higher scale silently shrinks the quiet zone below the four modules the standard asks for. Render only through `ToPNGBytes`/`ToSVGBytes`: the `PNG`/`SVG` pair writes files, and a Vercel function's filesystem is read-only outside `/tmp`, so a file round trip would be green locally and broken in production. `size` is a **ceiling, not an exact dimension** — whole pixels per module means a 512-pixel request yields the largest whole multiple that fits, clamped up to one pixel per module for a code with more modules than the requested pixels. Error-correction level, quiet zone and the logo (`qr_logo_url` and four other `link` columns stay dormant) all wait for the logo decision together: `WithLogo` validates the size against the ECC budget itself, so the obstacle is sourcing the image — a URL is the SSRF surface golden rule 3 names, and an upload needs Supabase Storage.
- **`GET /v1/links/{link_id}/qr` is the first `/v1` operation that answers with something other than JSON.** Huma writes a `[]byte` `Body` straight to the response and returns *before* the marshalling path that would set a `Content-Type`, so the media type travels as a `header:"Content-Type"` field on the output struct — and `huma.Operation.Responses["200"].Content` has to be written out by hand, or `processOutputType` fills in `application/json` and the generated TypeScript client tries to parse a PNG.
```

- [ ] **Step 4: Narrow doc 06's sketch**

In `docs/planning/06-api-design.md`, replace line 140:

```
- `GET /v1/links/{link_id}/qr` — QR image; query params for size/ECC level/margin/logo/colors (override the link's stored defaults); returns `image/png` or `image/svg+xml` directly based on `?format=`.
```

with:

```
- `GET /v1/links/{link_id}/qr` — QR image; returns `image/svg+xml` (the default) or `image/png` directly based on `?format=`. Settled 2026-09-09, narrower than this sketch: the parameters are `format`, `size` (64–2048, default 512, **PNG only** — a size on an SVG request is a 422, not a silent no-op) and `fg`/`bg` as `rrggbb` with or without a leading `#`. Error-correction level, quiet-zone margin and the logo are **not** parameters — all three exist mainly to serve the logo case, which is deferred, so `medium` and the standard four-module quiet zone are fixed. There are no per-link stored defaults to override either: the five remaining `qr_*` columns stay dormant because nothing writes them, so "query params override the link's stored defaults" describes an override of something that does not exist. See `docs/superpowers/specs/2026-09-09-link-qr-code-design.md`.
```

- [ ] **Step 5: Verify the whole tree**

Run: `pnpm format:check && pnpm lint && pnpm typecheck && pnpm --filter @kurze-url/web test && cd apps/api && gofmt -l . && go vet ./... && go test ./...` Expected: everything clean and green. The e2e suite runs against Vercel previews, not locally — it is not part of this gate.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-qr -m "docs: record the qr endpoint"
```

---

## Notes for the executor

- **This plan adds no migration.** The `qr_*` columns have existed since 2026-09-02 and stay dormant, so the "a branch that adds a migration cannot pass e2e until it is applied to Preview by hand" constraint does not apply here.
- **Do not add an audit entry.** A download is a read, and no read on this surface is audited.
- **Do not touch the redirect path.** `analytics.ExtractDimensions` already reads `?qr=1`; nothing there changes.
- **`apps/web/src/routeTree.gen.ts`** does not change — no route is added, only a component inside an existing one.
- **One open question the spec raises and does not answer** stays open after this plan: whether a slug change should warn, or be refused once a code has been downloaded. Do not invent an answer; `CLAUDE.md`'s updated QR bullet records the hazard.
