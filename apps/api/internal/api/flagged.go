package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/pages"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// flaggedRecheckTimeout bounds the one wait golden rule 2 allows on the
// redirect path, and only for a flagged link without a fresh confirmation:
// Google's terms forbid blocking it on stale data, and forwarding it unchecked
// would be worse. An active link never reaches the re-check.
const flaggedRecheckTimeout = 2 * time.Second

// unavailableRetryAfter is what the neutral page tells a browser and a
// crawler: long enough that a retry finds Google answering again, short enough
// that a visitor might still try.
const unavailableRetryAfter = "300"

// flaggedRechecks shares one Safe Browsing call between concurrent redirects of
// the same flagged link on this instance, so a link that is being shared
// widely costs one lookup per instance per confirmation window, not one per
// visitor. The key includes the destination, so a re-check started before a
// PATCH never answers for the URL that replaced it.
var flaggedRechecks singleflight.Group

// admit reports whether a resolved link may be followed, and writes the
// refusal itself when it may not. unavailable decides in its usual order —
// expiry, then state — and only its flagged answer needs more than a page. An
// active link returns here without calling anything. hostname and slug name
// the link's redirect-cache entry, which a clean re-check may have to evict.
func (d Deps) admit(
	w http.ResponseWriter, r *http.Request, locale pages.Locale,
	l link.Cached, hostname, slug string, now time.Time,
) bool {
	status, kind, blocked := unavailable(l, now)
	switch {
	case !blocked:
		return true
	case kind == pages.KindFlagged:
		return d.admitFlagged(w, r, locale, l, hostname, slug)
	default:
		pages.RenderError(w, status, locale, kind)
		return false
	}
}

// admitFlagged decides a flagged link:
//
//   - a confirmation younger than thirty minutes shows the block page, 403;
//   - otherwise Google is asked once, at most two seconds. Threats confirm the
//     block; a clean answer forwards the visitor exactly as an active link
//     would, while the re-check lifts the flag in the background (the sweep
//     lifts it if that goroutine is lost);
//   - an error, a timeout or no scanner answers the neutral 503, which says
//     nothing about the destination, because without a fresh confirmation
//     the terms forbid calling it unsafe.
func (d Deps) admitFlagged(
	w http.ResponseWriter, r *http.Request, locale pages.Locale,
	l link.Cached, hostname, slug string,
) bool {
	ctx := r.Context()

	if threats, ok := d.threatConfirmation(ctx, l.ID); ok {
		pages.RenderFlagged(w, locale, threats)
		return false
	}

	result, err := d.recheckFlagged(ctx, l, hostname, slug)
	if err != nil {
		w.Header().Set("Retry-After", unavailableRetryAfter)
		pages.RenderError(w, http.StatusServiceUnavailable, locale, pages.KindUnavailable)
		return false
	}
	if len(result.ThreatTypes) > 0 {
		pages.RenderFlagged(w, locale, result.ThreatTypes)
		return false
	}
	return true
}

// threatConfirmation reads a flagged link's confirmation. A Redis failure
// reads as "none" and is a Warn: the caller then asks Google, which costs a
// wait rather than a wrong answer.
func (d Deps) threatConfirmation(ctx context.Context, linkID uuid.UUID) ([]string, bool) {
	if d.Cache == nil {
		return nil, false
	}
	threats, ok, err := d.Cache.ThreatConfirmation(ctx, linkID.String())
	if err != nil {
		d.Log.Warn("safe browsing confirmation read failed, re-checking", "error", err, "link_id", linkID)
		return nil, false
	}
	return threats, ok
}

// recheckFlagged asks Google about one flagged link's destination, sharing
// the call with every concurrent redirect of the same link and destination.
//
// The shared call runs on a context detached from the request that started
// it: the first visitor closing the tab must not fail the check for everyone
// waiting on it. A visitor who leaves stops waiting; the call goes on.
func (d Deps) recheckFlagged(ctx context.Context, l link.Cached, hostname, slug string) (scanning.Result, error) {
	target := scanTarget{LinkID: l.ID, TeamID: l.TeamID, URL: l.DestinationURL}
	detached := context.WithoutCancel(ctx)

	outcome := flaggedRechecks.DoChan(l.ID.String()+"|"+l.DestinationURL, func() (any, error) {
		result, err := d.recheck(detached, target, hostname, slug)
		return result, err
	})

	select {
	case <-ctx.Done():
		return scanning.Result{}, ctx.Err()
	case shared := <-outcome:
		if shared.Err != nil {
			return scanning.Result{}, shared.Err
		}
		result, ok := shared.Val.(scanning.Result)
		if !ok {
			return scanning.Result{}, errNoVerdict
		}
		return result, nil
	}
}

// recheck is the one call concurrent redirects share, bounded by
// flaggedRecheckTimeout, and everything that follows from its answer happens
// here, once per re-check rather than once per waiting visitor: the failure
// is logged once, threats set the confirmation with one SET, and a clean
// answer starts one background unflag.
//
// A clean answer for a link Postgres already has as active means the redirect
// read a stale cache entry: another instance lifted the flag while this one
// still had the link cached. applyVerdict then changes nothing and evicts
// nothing, so the eviction happens here, one DEL per clean re-check, or every
// burst of visitors would ask Google again until the entry expired.
//
// The SET shares the two seconds with the Google call, so the wait never
// exceeds them; a SET that runs out of time is a Warn, and the next redirect
// asks again. It recovers a panic itself, because singleflight re-raises one
// from DoChan on a goroutine of its own, where nothing can catch it.
func (d Deps) recheck(ctx context.Context, target scanTarget, hostname, slug string) (result scanning.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
			d.Log.Error("safe browsing re-check panicked", "error", err, "link_id", target.LinkID)
		}
	}()

	checkCtx, cancel := context.WithTimeout(ctx, flaggedRecheckTimeout)
	defer cancel()

	result, err = d.checkTarget(checkCtx, target)
	if err != nil {
		d.logCheckFailure(err, "link_id", target.LinkID)
		return scanning.Result{}, err
	}

	if len(result.ThreatTypes) > 0 {
		d.confirmThreats(checkCtx, target.LinkID, result.ThreatTypes, result.ValidFor)
		return result, nil
	}

	d.inBackground(ctx, target.LinkID, func(ctx context.Context) {
		outcome, err := d.applyVerdict(ctx, target, result)
		switch {
		case err != nil:
			d.logApplyFailure(err, target.LinkID)
		case outcome == verdictUnchanged:
			d.invalidateLink(ctx, hostname, slug)
		}
	})
	return result, nil
}
