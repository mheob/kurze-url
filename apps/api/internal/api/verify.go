package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/analytics"
	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/pages"
)

// HandleVerifyForm serves GET /{slug}/verify. It exists so a bookmarked or
// reloaded interstitial still works; GET /{slug} renders the same form.
func (d Deps) HandleVerifyForm(w http.ResponseWriter, r *http.Request) {
	locale := pages.Negotiate(r.Header.Get("Accept-Language"))
	slug := chi.URLParam(r, "slug")

	if _, _, ok := d.loadProtectedLink(w, r, locale, slug); !ok {
		return
	}

	pages.RenderPasswordPrompt(w, http.StatusOK, locale, "/"+slug+"/verify", false)
}

// HandleVerifySubmit serves POST /{slug}/verify. It carries its own, much
// tighter rate limit than the redirect path: Argon2id alone is not enough
// against a short password, so the number of guesses is capped per link and
// client address.
func (d Deps) HandleVerifySubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locale := pages.Negotiate(r.Header.Get("Accept-Language"))
	slug := chi.URLParam(r, "slug")
	hostname := Hostname(r.Host)
	ip := ClientIP(r)
	now := d.now()

	rateKey := "rl:pwverify:" + hostname + ":" + slug + ":" + ip
	allowed, _, err := d.Cache.Allow(ctx, rateKey, d.Config.PasswordRateLimitPerMin, time.Minute)
	if err != nil {
		// Unlike the redirect path this fails closed: an unbounded number of
		// password guesses is a worse outcome than a temporarily unusable
		// protected link.
		d.Log.Error("password rate limit unavailable, failing closed", "error", err)
		allowed = false
	}
	if !allowed {
		w.Header().Set("Retry-After", "60")
		pages.RenderError(w, http.StatusTooManyRequests, locale, pages.KindRateLimited)
		return
	}

	resolved, hash, ok := d.loadProtectedLink(w, r, locale, slug)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		pages.RenderPasswordPrompt(w, http.StatusUnauthorized, locale, "/"+slug+"/verify", true)
		return
	}

	valid, err := auth.VerifyPassword(hash, r.PostFormValue("password"))
	if err != nil {
		// A malformed stored hash is our bug, not a wrong password.
		d.Log.Error("stored password hash is unusable", "link_id", resolved.ID, "error", err)
		pages.RenderError(w, http.StatusInternalServerError, locale, pages.KindServerError)
		return
	}
	if !valid {
		pages.RenderPasswordPrompt(w, http.StatusUnauthorized, locale, "/"+slug+"/verify", true)
		return
	}

	if resolved.AnalyticsEnabled {
		unique, err := d.Cache.MarkUniqueVisit(ctx, resolved.ID.String(), analytics.Day(now),
			analytics.VisitorHash(d.Config.VisitorSalt, ip, r.UserAgent(), now),
			d.Config.UniqueVisitorTTL)
		if err != nil {
			d.Log.Error("unique-visitor dedup failed", "error", err)
		}
		d.Recorder.Record(resolved.ID, now, analytics.ExtractDimensions(r), unique)
	}

	d.writeRedirect(w, r, resolved)
}

// loadProtectedLink reads the link straight from Postgres — the password hash
// is deliberately never cached, so the verify path cannot use the link cache.
// It writes the response itself and reports false when the caller must stop.
func (d Deps) loadProtectedLink(
	w http.ResponseWriter,
	r *http.Request,
	locale pages.Locale,
	slug string,
) (link.Cached, string, bool) {
	row, err := d.Queries.GetLinkForVerify(r.Context(), db.GetLinkForVerifyParams{
		Hostname: Hostname(r.Host),
		Slug:     slug,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		pages.RenderError(w, http.StatusNotFound, locale, pages.KindNotFound)
		return link.Cached{}, "", false
	}
	if err != nil {
		d.Log.Error("verify database lookup failed", "error", err, "slug", slug)
		pages.RenderError(w, http.StatusInternalServerError, locale, pages.KindServerError)
		return link.Cached{}, "", false
	}

	// Verifying a password against a link that has none is meaningless; treat
	// it as a missing page rather than revealing that the link exists.
	if row.PasswordHash == nil || *row.PasswordHash == "" {
		pages.RenderError(w, http.StatusNotFound, locale, pages.KindNotFound)
		return link.Cached{}, "", false
	}

	resolved := link.Cached{
		ID:             row.ID,
		TeamID:         row.TeamID,
		DestinationURL: row.DestinationURL,
		RedirectType:   int(row.RedirectType),
		State:          row.State,
		ExpiresAt:      row.ExpiresAt,
		HasPassword:    true,

		AnalyticsEnabled: row.AnalyticsEnabled,
	}

	// State is checked before the password so a disabled or flagged link never
	// becomes an oracle for guessing its password.
	if status, kind, blocked := unavailable(resolved, d.now()); blocked {
		pages.RenderError(w, status, locale, kind)
		return link.Cached{}, "", false
	}

	return resolved, *row.PasswordHash, true
}
