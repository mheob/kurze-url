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
