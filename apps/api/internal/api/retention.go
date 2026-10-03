package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"
)

// retentionBudget bounds the statement so a hung delete fails rather than
// hanging indefinitely. The workflow's own step failing withholds the heartbeat
// either way. Which of the two timeouts fires first — this one or the
// platform's function ceiling — depends on the platform's configuration, which
// this repository does not pin.
const retentionBudget = 30 * time.Second

type retentionBody struct {
	Deleted int64 `json:"deleted"`
	// OldestKept is in the response because it is the one value proving this
	// job and the stats endpoint agree about where the window starts. Someone
	// debugging that months from now can read it off a single curl instead of
	// reasoning about two constants in two languages.
	OldestKept string `json:"oldest_kept"`
	// The audit pair sits beside the click pair under prefixed names rather
	// than regrouping all four, because the workflow's logs are read by
	// deleted and oldest_kept and those two keys must keep meaning what they
	// meant. AuditOldestKept is the audit log endpoint's retained_since, for
	// the same reason OldestKept is the stats endpoint's floor.
	AuditDeleted    int64  `json:"audit_deleted"`
	AuditOldestKept string `json:"audit_oldest_kept"`
}

// HandleRetention answers POST /internal/retention: the daily deletion that
// keeps both retention periods true rather than merely promised —
// docs/planning/01-architecture.md's "90-day automatic deletion, confirmed"
// for the click rollup, and AuditRetentionYears for the audit log.
//
// The two deletes run in that order and stop at the first failure: a failed
// statement is logged at error level under its own message, the response is
// a 500, and the remaining delete is skipped. The response is therefore
// either the whole report or none of it. Both statements are idempotent, so
// the next day's run, or a manual retry, finishes what a failed one left.
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

	// One reading of the clock for both floors, so a run that straddles
	// midnight cannot compute them from two different days.
	now := d.now()
	oldestKept := retentionCutoff(now)
	auditOldestKept := auditRetentionFloor(now)

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
	// It is logged before the audit delete rather than after it, because this
	// delete has already committed: if the next one fails, the log still says
	// truthfully what this run did.
	d.Log.Info("analytics retention ran", "deleted", deleted,
		"oldest_kept", oldestKept.Format(dayLayout))

	// No team_id here either, for the reason given above: the audit log's
	// retention period is the same for every Verein, and the job acts for the
	// instance.
	auditDeleted, err := d.Queries.DeleteExpiredAuditLog(ctx, auditOldestKept)
	if err != nil {
		d.Log.Error("audit log retention failed", "error", err,
			"oldest_kept", auditOldestKept.Format(dayLayout))
		http.Error(w, "retention failed", http.StatusInternalServerError)
		return
	}

	// Info for the same reason as the line above, over a longer stretch: the
	// audit log's first entries are from September 2026, so for about two
	// years this line reporting zero is the only sign the job reached it.
	d.Log.Info("audit log retention ran", "deleted", auditDeleted,
		"oldest_kept", auditOldestKept.Format(dayLayout))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(retentionBody{
		Deleted:         deleted,
		OldestKept:      oldestKept.Format(dayLayout),
		AuditDeleted:    auditDeleted,
		AuditOldestKept: auditOldestKept.Format(dayLayout),
	})
}

// retentionCutoff is the oldest day the stats endpoint will still serve, and
// therefore the oldest day this job must keep. It delegates to retentionFloor
// rather than restating its arithmetic, because the endpoint's own floor
// comes from the same constant — two definitions of one boundary would
// eventually disagree, and the disagreement is silent in both directions. By
// construction, not by coincidence, this is the stats endpoint's floor.
func retentionCutoff(now time.Time) time.Time {
	return retentionFloor(now)
}
