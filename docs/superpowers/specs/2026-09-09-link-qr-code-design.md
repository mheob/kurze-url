# Link QR Codes — Design

**Status:** approved 2026-09-09 **Amends:** `CLAUDE.md` (the API-surface summary gains the endpoint's shape; a new non-obvious constraint about what the QR encodes and why the browser never generates one), `docs/planning/06-api-design.md` (`GET /v1/links/{link_id}/qr` gains its settled parameter set, which is narrower than the sketch there).

The eleventh implementation spec. Ten plans have merged: a maintainer creates a team, a Verein claims a custom domain, links redirect from `go.kurze-url.app`, every authenticated page is addressed by the team's slug, and a link can be protected with a password.

QR codes are the older debt. The `link` table has carried six `qr_*` columns since the initial schema on 2026-09-02, generated into `db.Link` and read by nothing. `analytics.QRQueryParam` has existed just as long, and its own comment names the missing piece: "The QR generator (plan 2) appends `?qr=1` to the short URL it encodes; nothing else can distinguish a scan from a normal click." Plan 2 did not build it, and no plan since has. So `link_click_stats` has been recording `qr_vs_regular = regular` on every click for eight plans, because nothing in the system can produce the other value.

This spec builds the generator. It is deliberately narrower than `docs/planning/06-api-design.md`'s sketch, and the narrowing is the substance of the design rather than a shortcut.

## Goal

A team member can download a link's QR code from the dashboard as SVG or PNG, in the Verein's colours, at a size that suits the medium — and a scan of that code is distinguishable from a normal click in the link's statistics.

## Scope

### In scope

- `GET /v1/links/{link_id}/qr`, returning raw image bytes.
- A new `internal/qr` package: the matrix, the two renderers, and the contrast rule. No HTTP.
- Three query parameters: `format`, `size`, and the two colours.
- A rate limit on the endpoint, and the environment variable that carries it.
- A QR card on the link detail route with a format choice, a colour picker, a size control and a live preview.
- The contrast rule mirrored in the browser, for the same reason the password policy is.

### Out of scope, and where each lands

- **The logo.** `qr_logo_url` stays unused. Note precisely what is and is not the obstacle: `go-qr` has `WithLogo`, and its own documentation says the logo is "validated against the ECC budget" — so the rendering, and the module-budget check `CLAUDE.md` asks for, are both free. What is not free is **where the image comes from**. A URL means fetching a third-party image on every render, which is exactly the SSRF surface golden rule 3 names, on an authenticated endpoint with no allowlist; it needs `internal/destination`'s guard with a re-check at fetch time, a size bound, a cache, and an answer for what happens when the image disappears. An upload avoids all of that and needs Supabase Storage instead — buckets, limits, cleanup on link deletion — which is a subsystem of its own. A Verein's first need is a scannable code, not a branded one, so this waits for that decision rather than for the drawing.
- **Error-correction level and quiet-zone margin.** Both are configurable in the original feature list, and both exist almost entirely to serve the logo case: a logo eats modules, so it demands a higher correction level and a watched margin. Without a logo, `medium` and the standard four-module quiet zone are correct, and a caller who changes them is far likelier to produce a code that does not scan than one that scans better. They come back with the logo.
- **Per-link stored QR defaults.** The five remaining `qr_*` columns stay dormant. Nothing sets them today — `PATCH /v1/links/{link_id}` has no QR field — so doc 06's "query params override the link's stored defaults" describes an override of something that does not exist. Waking them means a `PATCH` surface, its validation, and a frontend for it; the payoff is remembering a colour, which the browser can do without a schema.
- **`GET /v1/links/{link_id}/stats`.** The other unbuilt endpoint. Its own spec, next.

## Global constraints

Inherited and not re-litigated here:

- No RLS. Every query filters by `team_id`; the check lives in Go.
- A non-member gets 404, never 403. So does a member whose `link_id` belongs to another team.
- The redirect path is the hot path. This spec adds nothing to `GET /{slug}`.
- No hardcoded user-facing string; English and German ship together.
- WCAG 2.1 AA, gated in CI at two levels.
- Errors use Huma's default RFC 9457 `application/problem+json`; a typed value travels in `huma.ErrorDetail{Location, Value}`.
- Conventional Commits; `pnpm format` before every commit; the hooks are not bypassed.

## The API surface

**`GET /v1/links/{link_id}/qr`** authorizes through `authz.LinkViewerScope`. Generating an image reads a link and changes nothing, so the viewer role is the right floor — unlike the password endpoints, which take `LinkEditorScope` because they decide who reaches the link.

It is this project's first `/v1` operation that answers with something other than JSON. Huma v2.39.1 supports it directly: when an output struct's `Body` field is a `[]byte`, `huma.go`'s handler writes it to the response and returns before the marshalling path, so the `Content-Type` must come from a response header field on the same struct rather than from Huma's content negotiation.

```go
type LinkQROutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}
```

`Content-Disposition` is `attachment; filename="<slug>.<ext>"`. A Verein downloading codes for six links should not end up with `qr.png` beside `qr(3).png` and no way to tell which is the Sommerfest.

The operation must declare its response content types in the OpenAPI document, or the generated TypeScript client will type the body as JSON and `packages/api-client` will try to parse a PNG.

`internal/qr` renders through `go-qr`'s in-memory functions, `ToPNGBytes` and `ToSVGBytes`, never through its file-writing `PNG`/`SVG` pair. That is not a preference: a Vercel function's filesystem is read-only outside `/tmp`, so a file round-trip would be a bug that only appears in production. Colours come from `WithLight` and `WithDark`, which the library applies to both formats, so one configuration serves both renderers and there is no second colour path to keep in step.

### What the code encodes

`<scheme>://<hostname>/<slug>?qr=1` — the link's own short URL, with the marker `analytics.QRQueryParam` already reads on the redirect path.

Never the destination. That is what makes changing a destination safe for a code already printed on two hundred flyers, and `CLAUDE.md` records it as a standing constraint.

The marker is the reason this feature is worth more than its size suggests: it is the only thing that can populate `qr_vs_regular`, a dimension the redirect path has been writing a constant into since the analytics went in.

### Parameters

| Parameter  | Values             | Default                |
| ---------- | ------------------ | ---------------------- |
| `format`   | `svg`, `png`       | `svg`                  |
| `size`     | pixels, 64 to 2048 | 512                    |
| `fg`, `bg` | `#rrggbb`          | `#000000` on `#ffffff` |

**`svg` is the default** because the primary use is print — a flyer, a poster, a newsletter — where a vector scales without a decision and a raster does not. A caller who wants a raster asks for one.

**`size` applies to PNG only.** A vector has no pixel size, so `size` with `format=svg` is a **422**, not a silent no-op. A parameter that accepts a value and does nothing with it is a lie to every caller without a frontend to hide it — the CLI, a script, anyone using the generated client. The dashboard additionally hides the control when SVG is selected, which is a different job: the API protects callers, the interface protects a person from a control whose movement has no effect. Both, not either.

**The upper bound on `size` is a memory limit, not a style rule.** At 10000 pixels the RGBA buffer alone is 400 MB, on a function that does not have it. 2048 covers A4 at 300 dpi with room to spare.

**`size` is a ceiling, not an exact dimension, and that is forced by the format.** `go-qr` renders from a scale in whole pixels per module, so an image is always `(modules + 2 × border) × scale` pixels on a side. A 25-module code with the standard four-module quiet zone is 33 modules wide; at `size=512` the largest whole scale that fits is 15, giving 495 pixels. The endpoint therefore picks the largest scale whose output does not exceed `size` and returns that. Rounding _up_ instead would break the memory bound the previous paragraph exists to enforce, and refusing sizes that do not divide evenly would make the parameter useless, since the module count depends on how long the URL is and the caller cannot know it. A QR is square and self-similar, so a code 17 pixels smaller than requested is the same code; the plan should say so in the endpoint's `doc:` string rather than leave a caller measuring the result.

The floor of 64 is where this stops being cosmetic: below roughly two pixels per module, a phone camera cannot resolve the modules at all.

### The contrast rule

A colour pair below a minimum luminance contrast is refused with a **422** carrying `huma.ErrorDetail{Location: "query.fg", Value: "<reason>"}`, the same typed-value pattern `deleteDomain` established and the password policy follows.

This is not decoration. A Verein picks its own yellow on white, the code renders, it looks entirely fine on a monitor at 100%, and it does not scan — and the camera that fails to read it says nothing about why. The failure lands weeks later, on printed material.

The threshold is **4.5:1**, computed as WCAG computes relative-luminance contrast. That is WCAG's floor for normal text, not its looser 3:1 for graphics, and the stricter of the two is the right one here: a QR module is smaller than a glyph and is read by a phone camera in a Verein's badly lit clubhouse rather than by an eye on a calibrated screen. Choosing the graphics threshold would let through pairs that pass a specification and fail a scan.

Unlike the password policy's word list, this is a closed formula: there is nothing to curate and nothing to drift.

## Rate limiting

`RATE_LIMIT_QR_PER_MIN=30`, per user, keyed `rl:qr:<user id>`, checked with the existing `Allow`.

Rendering a QR is a small bitmap, not an Argon2id hash, and the endpoint is membership-bound — so this is a consistency measure rather than a defence against amplification, and it should be recorded as one. Plenty of member-driven reads here carry no limit at all: `GET /v1/me`, `GET /v1/links`, the audit log. What sets this one apart is that its cost scales with a parameter the caller chooses — a 2048-pixel render is not a 512-pixel render — and that the size bound alone caps the peak, not the rate. A limit was chosen over the alternative of leaving the only parameter-scaled read unbounded.

Two things it carries from the rate-limit work settled on 2026-09-08 and corrected on 2026-09-09:

- **A `<= 0` guard in the handler, from the first commit.** Every limiter script compares `>= limit`, so a zero refuses everything rather than nothing. `allowRedirect` shipped without that guard and a zero would have answered 429 to every visitor on the instance; that was fixed in #67. A new limiter must not repeat it.
- **`.env.example` says what the value protects, what it does not, and what `0` means.** Not one of those three is optional in that file.

## The browser never generates a QR code

The dashboard offers a live preview, and the obvious implementation — re-request the image on every colour change and every drag of the size control — would issue dozens of requests in seconds and exhaust a 30-per-minute limit almost immediately. The feature would be broken by the limit introduced in the same change.

The tension is not real, because **neither colour nor size changes the QR matrix**. The matrix is determined by the encoded string and the error-correction level, both fixed here. Colours are attributes in the SVG; size is a number in its viewport.

So the card fetches the SVG **once** per link, and the preview restyles that document locally: colours by setting attributes, size by scaling. The API is called again only for the actual download, in the chosen format. Two requests per link instead of fifty.

The property that matters is not the request count — it is that **the browser never implements QR generation**. It restyles a document the API produced. A second generator in TypeScript would drift from the Go one, and unlike the password policy, where drift costs a message arriving a round trip late, drift in an image means the preview shows something the download does not deliver.

The contrast rule _is_ mirrored in the browser, for immediate feedback, and that mirror is safe for the reason given above: it is a formula, not a corpus.

## Frontend

A QR card on `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`, beside the password card, with:

- a format choice, SVG or PNG
- a colour picker for foreground and background
- a size control, hidden when SVG is selected
- the live preview described above
- a download control

A contrast violation caught in the browser renders under the colour control and does not request the download. A 422 the mirror did not predict renders the same way, keyed off `ErrorDetail.Value` through the existing `classifyApiError`, exactly as the password card does — including a fallback for a reason token this build does not recognise.

New keys in both `apps/web/src/i18n/locales/en.json` and `de.json`.

## Testing

### Go

- **The core test decodes the output.** Render a code, read the image back, and assert the decoded string is the link's short URL with `?qr=1`. Comparing bytes or file lengths tests `piglig/go-qr`, not this code; decoding tests the one property a QR has to have.
- Both formats, and that `format=svg` with `size` is a 422 rather than an ignored parameter.
- The contrast rule: an accepted pair, a refused pair, and the 422's `Location` and `Value`.
- The size bound at both ends.
- `LinkViewerScope`: a viewer succeeds where the password endpoints would refuse, and another team's link is 404 rather than 403.
- The rate limit: refusal at the cap, `0` disabling the axis, and the fail-closed behaviour the handler chooses.

### Web

Vitest and RTL for the card: the contrast mirror refusing before a request, the size control hiding under SVG, the preview restyling without a second fetch, and a 422 the mirror missed still rendering. One Storybook story per state, which is what carries the accessibility check.

### E2E

The dashboard side only: open a link, download a code. The image itself is not asserted in e2e — that is what the Go decode test is for.

## Consequences to expect

- **`qr_vs_regular` starts producing two values.** Every row written before this ships says `regular`, including rows for links whose codes were shared by other means. The stats endpoint, specced next, will need to say so rather than imply the split is historical.
- **A printed code breaks when the slug changes, and nothing says so.** The encoded URL contains the slug. Changing a link's _destination_ is safe and is the entire reason the code encodes the short URL — but changing its _slug_ silently invalidates every code already on a flyer. `PATCH /v1/links/{link_id}` allows a slug change today and warns about nothing, which was harmless while no codes existed. It stops being harmless here. Whether that warrants a warning in the edit form, or a refusal once a code has been downloaded, is a question this spec raises and does not answer.
- **Five schema columns stay dormant, now deliberately.** Written down here so the next reader finds a decision rather than an oversight.
- **The first non-JSON operation in the OpenAPI document.** Whatever the generator does with it becomes the precedent for `GET /links/{id}/stats`'s eventual CSV export, if that ever arrives.
