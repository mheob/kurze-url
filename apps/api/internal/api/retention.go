package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"
)

// retentionBudget bounds the delete. The workflow calling this has its own
// timeout, and a statement sweeping a table nobody has pruned in months is
// exactly the shape that outlives one — better to fail visibly here, where the
// heartbeat withholds its ping, than to have the caller give up on a statement
// still running.
const retentionBudget = 30 * time.Second

type retentionBody struct {
	Deleted int64 `json:"deleted"`
	// OldestKept is in the response because it is the one value proving this
	// job and the stats endpoint agree about where the window starts. Someone
	// debugging that months from now can read it off a single curl instead of
	// reasoning about two constants in two languages.
	OldestKept string `json:"oldest_kept"`
}

// HandleRetention answers POST /internal/retention: the daily deletion that
// makes docs/planning/01-architecture.md's "90-day automatic deletion,
// confirmed" true rather than merely promised.
//
// The authorization below is HandleDeepHealth's, deliberately unchanged — the
// same 404 for a missing and a wrong token, the same constant-time compare,
// the same empty-means-disabled rule. See that function for why each one.
// What differs is only the consequence of getting it wrong: that endpoint
// leaks dependency status, this one deletes data.
//
// It is not in the OpenAPI document and not under /v1. Like /health/deep it
// answers on every hostname, because it sits on the root router above the
// hostname split — the token is the security boundary here, not the hostname.
func (d Deps) HandleRetention(w http.ResponseWriter, r *http.Request) {
	if d.Config.RetentionToken == "" ||
		subtle.ConstantTimeCompare(
			[]byte(r.Header.Get("X-Retention-Token")),
			[]byte(d.Config.RetentionToken),
		) != 1 {
		http.NotFound(w, r)
		return
	}

	oldestKept := retentionCutoff(d.now())

	ctx, cancel := context.WithTimeout(r.Context(), retentionBudget)
	defer cancel()

	// No team_id, and that is correct here. Everywhere else in this codebase a
	// query without a tenancy filter is a data-leak bug; this one acts for the
	// instance rather than for a caller, and scoping it to a team would make
	// the retention promise depend on who happened to call.
	deleted, err := d.Queries.DeleteExpiredClickStats(ctx, oldestKept)
	if err != nil {
		d.Log.Error("analytics retention failed", "error", err,
			"oldest_kept", oldestKept.Format(dayLayout))
		http.Error(w, "retention failed", http.StatusInternalServerError)
		return
	}

	// Info, not Debug: for the first eighty days this job runs, this line is
	// the only thing distinguishing "ran and found nothing" from "did not run".
	d.Log.Info("analytics retention ran", "deleted", deleted,
		"oldest_kept", oldestKept.Format(dayLayout))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(retentionBody{
		Deleted:    deleted,
		OldestKept: oldestKept.Format(dayLayout),
	})
}

// retentionCutoff is the oldest day the stats endpoint will still serve, and
// therefore the oldest day this job must keep. It is derived from
// RetentionDays rather than restated, because the endpoint's own floor comes
// from the same constant — two definitions of one boundary would eventually
// disagree, and the disagreement is silent in both directions.
func retentionCutoff(now time.Time) time.Time {
	return dayOf(now).AddDate(0, 0, -(RetentionDays - 1))
}
