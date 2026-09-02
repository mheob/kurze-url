package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/pages"
)

// HandleRedirect serves GET /{slug} on a short-link hostname. This is the hot
// path: it never waits on anything optional. Click recording goes into an
// in-memory buffer, and a Redis failure degrades the response rather than
// failing it.
func (d Deps) HandleRedirect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locale := pages.Negotiate(r.Header.Get("Accept-Language"))

	slug := chi.URLParam(r, "slug")
	hostname := Hostname(r.Host)
	ip := ClientIP(r)
	now := d.now()

	if !d.allowRedirect(ctx, ip) {
		w.Header().Set("Retry-After", "60")
		pages.RenderError(w, http.StatusTooManyRequests, locale, pages.KindRateLimited)
		return
	}

	cacheKey := link.CacheKey(hostname, slug)
	visitor := analytics.VisitorHash(d.Config.VisitorSalt, ip, r.UserAgent(), now)
	day := analytics.Day(now)

	lookup, err := d.Cache.LookupForRedirect(ctx, cacheKey, visitor, day, d.Config.UniqueVisitorTTL)
	if err != nil {
		// Degrade to Postgres rather than failing the redirect.
		d.Log.Error("redirect cache lookup failed", "error", err, "hostname", hostname)
		lookup = cache.Lookup{}
	}

	if lookup.NegativelyCached {
		pages.RenderError(w, http.StatusNotFound, locale, pages.KindNotFound)
		return
	}

	resolved := lookup.Link
	unique := lookup.UniqueVisit

	if !lookup.Found {
		resolved, unique, err = d.resolveFromDatabase(ctx, hostname, slug, cacheKey, visitor, day)
		if errors.Is(err, pgx.ErrNoRows) {
			pages.RenderError(w, http.StatusNotFound, locale, pages.KindNotFound)
			return
		}
		if err != nil {
			d.Log.Error("redirect database lookup failed", "error", err, "hostname", hostname, "slug", slug)
			pages.RenderError(w, http.StatusInternalServerError, locale, pages.KindServerError)
			return
		}
	}

	if status, kind, blocked := unavailable(resolved, now); blocked {
		pages.RenderError(w, status, locale, kind)
		return
	}

	if resolved.HasPassword {
		// Not a click yet — the click is recorded when the password verifies.
		pages.RenderPasswordPrompt(w, http.StatusOK, locale, "/"+slug+"/verify", false)
		return
	}

	if resolved.AnalyticsEnabled {
		d.Recorder.Record(resolved.ID, now, analytics.ExtractDimensions(r), unique)
	}
	d.writeRedirect(w, r, resolved)
}

// allowRedirect applies the per-IP redirect rate limit. It fails open: if
// Redis is unreachable the redirect still works, because availability of the
// redirect is the product. The failure is logged loudly instead.
func (d Deps) allowRedirect(ctx context.Context, ip string) bool {
	allowed, _, err := d.Cache.Allow(ctx, "rl:redirect:"+ip,
		d.Config.RedirectRateLimitPerMin, time.Minute)
	if err != nil {
		d.Log.Error("redirect rate limit unavailable, failing open", "error", err)
		return true
	}
	return allowed
}

func (d Deps) resolveFromDatabase(
	ctx context.Context,
	hostname, slug, cacheKey, visitor, day string,
) (link.Cached, bool, error) {
	row, err := d.Queries.GetLinkForRedirect(ctx, db.GetLinkForRedirectParams{
		Hostname: hostname,
		Slug:     slug,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if putErr := d.Cache.PutNotFound(ctx, cacheKey, d.Config.NotFoundCacheTTL); putErr != nil {
			d.Log.Error("negative cache write failed", "error", putErr)
		}
		return link.Cached{}, false, err
	}
	if err != nil {
		return link.Cached{}, false, err
	}

	resolved := link.Cached{
		ID:             row.ID,
		TeamID:         row.TeamID,
		DestinationURL: row.DestinationURL,
		RedirectType:   int(row.RedirectType),
		State:          row.State,
		ExpiresAt:      row.ExpiresAt,
		HasPassword:    row.HasPassword,

		AnalyticsEnabled: row.AnalyticsEnabled,
	}

	if err := d.Cache.PutLink(ctx, cacheKey, resolved, d.Config.LinkCacheTTL); err != nil {
		d.Log.Error("link cache write failed", "error", err)
	}

	// The lookup script had no link id to deduplicate against on a miss, so
	// the visitor is recorded here instead.
	unique, err := d.Cache.MarkUniqueVisit(ctx, resolved.ID.String(), day, visitor, d.Config.UniqueVisitorTTL)
	if err != nil {
		d.Log.Error("unique-visitor dedup failed", "error", err)
	}

	return resolved, unique, nil
}

// unavailable reports whether a link must not be followed, and with what.
func unavailable(l link.Cached, now time.Time) (int, pages.Kind, bool) {
	if l.ExpiresAt != nil && !now.Before(*l.ExpiresAt) {
		return http.StatusGone, pages.KindExpired, true
	}

	switch l.State {
	case "active":
		return 0, "", false
	case "disabled":
		return http.StatusGone, pages.KindDisabled, true
	case "expired":
		return http.StatusGone, pages.KindExpired, true
	case "flagged":
		return http.StatusForbidden, pages.KindFlagged, true
	default:
		return http.StatusGone, pages.KindDisabled, true
	}
}

func (d Deps) writeRedirect(w http.ResponseWriter, r *http.Request, l link.Cached) {
	status := http.StatusFound
	if l.RedirectType == http.StatusMovedPermanently {
		status = http.StatusMovedPermanently
	} else {
		// A 302 promises a fresh lookup every time; say so explicitly rather
		// than leaving it to an intermediary's heuristics.
		w.Header().Set("Cache-Control", "no-store")
	}

	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, l.DestinationURL, status)
}
