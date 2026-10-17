package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// backgroundScanTimeout bounds one best-effort check started after a write.
// Ten seconds rather than the redirect path's two: nobody waits on it, and a
// slow answer that still arrives is worth more than one the sweep has to
// repeat.
const backgroundScanTimeout = 10 * time.Second

// scanLockTimeout bounds how long applyVerdict waits for the link's row lock.
// A PATCH holds it for milliseconds. A holder that takes longer is an instance
// Vercel froze mid-verdict, which keeps the lock until its connection dies;
// unbounded, the sweep, which applies one link at a time, would wait on that
// link until its budget ran out, run after run. Far inside the sweep's budget
// and the background check's ten seconds: a timeout fails this one link, which
// stays due, and logs at Error like any other failed write.
const scanLockTimeout = 2 * time.Second

// afterCommitTimeout bounds the Redis work applyVerdict does once its
// transaction has committed. It is far below confirmationMargin, the minute a
// confirmation's TTL is shortened by, because the TTL is counted from the SET
// rather than from Google's answer.
const afterCommitTimeout = 3 * time.Second

const (
	// confirmationMaxAge is the thirty minutes of Google's terms: no warning
	// and no block on a verdict older than that.
	confirmationMaxAge = 30 * time.Minute
	// confirmationMargin keeps a confirmation from expiring in the same minute
	// the terms' window closes, whatever the clocks between here, Redis and
	// Google make of "thirty minutes".
	confirmationMargin = time.Minute
)

var (
	// errNoVerdict means a check came back without a verdict for the URL
	// asked about. A failure, never "clean"; scanning.Checker says why.
	errNoVerdict = errors.New("safe browsing returned no verdict for the destination")
	// errScanningOff means SAFE_BROWSING_API_KEY is unset, so there is no
	// checker to ask.
	errScanningOff = errors.New("safe browsing scanning is off")
)

// scanTarget is one link and the destination a check judged for it. TeamID
// travels with it so every write the verdict causes filters by tenant.
type scanTarget struct {
	LinkID uuid.UUID
	TeamID uuid.UUID
	URL    string
}

// verdictOutcome is what applyVerdict made of a verdict.
type verdictOutcome string

const (
	// verdictFlagged: an active link Google reports is flagged now.
	verdictFlagged verdictOutcome = "flagged"
	// verdictUnflagged: a flagged link Google no longer reports is active again.
	verdictUnflagged verdictOutcome = "unflagged"
	// verdictConfirmed: a flagged link is still reported; its confirmation was refreshed.
	verdictConfirmed verdictOutcome = "confirmed"
	// verdictUnchanged: an active link is still clean; only the check was recorded.
	verdictUnchanged verdictOutcome = "unchanged"
	// verdictStale: the destination changed after the check; the verdict was discarded.
	verdictStale verdictOutcome = "stale"
	// verdictSkipped: the link is no longer active or flagged; nothing was written.
	verdictSkipped verdictOutcome = "skipped"
	// verdictGone: the link was deleted before the verdict arrived.
	verdictGone verdictOutcome = "gone"
)

// applyVerdict writes one link's verdict, in one transaction:
//
//   - The link is read FOR UPDATE, so the check and the write are one
//     decision against any concurrent PATCH, waiting at most scanLockTimeout
//     for that lock.
//   - A verdict for a URL the link no longer points at is discarded: a newer
//     destination is waiting for its own check. So is one for a link that is
//     no longer active or flagged, which then becomes due again when it is
//     re-enabled.
//   - Otherwise the check is recorded, and the state follows the verdict:
//     threats on an active link flag it, a clean check on a flagged one lifts
//     the flag. Only those two transitions write a link_scan_result row and a
//     system-actor audit entry.
//
// After the commit, a flag sets the Redis confirmation and clears the cached
// link, and logs at Error, which is how the maintainer hears of it. Lifting a
// flag clears both. That work runs on its own short timeout, detached from
// ctx. A link that was deleted meanwhile is not an error.
//
// Every read and write here filters by target.TeamID, although the scanner
// belongs to no team: it always knows the link's team, so the tenancy rule
// costs nothing to keep.
func (d Deps) applyVerdict(ctx context.Context, target scanTarget, result scanning.Result) (verdictOutcome, error) {
	now := d.now()
	// Never nil: link_scan_result.threat_types is not null, pgx encodes a nil
	// slice as SQL NULL rather than an empty array, and the audit metadata
	// would carry a null where a list belongs.
	threats := result.ThreatTypes
	if threats == nil {
		threats = []string{}
	}

	var (
		outcome        verdictOutcome
		hostname, slug string
		lockTimeout    = strconv.FormatInt(scanLockTimeout.Milliseconds(), 10) + "ms"
	)
	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		if err := q.SetLocalLockTimeout(ctx, lockTimeout); err != nil {
			return err
		}
		current, err := q.GetLinkForScan(ctx, db.GetLinkForScanParams{
			ID: target.LinkID, TeamID: target.TeamID,
		})
		if err != nil {
			return err
		}
		hostname, slug = current.Hostname, current.Slug

		switch {
		case current.DestinationURL != target.URL:
			outcome = verdictStale
			return nil
		case current.State != "active" && current.State != "flagged":
			outcome = verdictSkipped
			return nil
		}

		state := current.State
		switch {
		case len(threats) > 0 && current.State == "active":
			state, outcome = "flagged", verdictFlagged
		case len(threats) == 0 && current.State == "flagged":
			state, outcome = "active", verdictUnflagged
		case len(threats) > 0:
			outcome = verdictConfirmed
		default:
			outcome = verdictUnchanged
		}

		if err := q.RecordLinkScan(ctx, db.RecordLinkScanParams{
			ID:                 target.LinkID,
			TeamID:             target.TeamID,
			State:              state,
			CheckedAt:          now,
			CheckedDestination: target.URL,
		}); err != nil {
			return err
		}
		if outcome != verdictFlagged && outcome != verdictUnflagged {
			return nil
		}

		verdict, action := "flagged", audit.ActionLinkFlagged
		if outcome == verdictUnflagged {
			verdict, action = "clean", audit.ActionLinkUnflagged
		}
		written, err := q.InsertLinkScanResult(ctx, db.InsertLinkScanResultParams{
			LinkID:         target.LinkID,
			TeamID:         target.TeamID,
			Verdict:        verdict,
			DestinationURL: target.URL,
			ThreatTypes:    threats,
			ScannedAt:      now,
		})
		if err != nil {
			return err
		}
		if written == 0 {
			// The link was read and locked under this team a moment ago, so
			// only a bug gets here. Rolling back keeps a state change from
			// landing without the row that says why; the link stays due.
			return fmt.Errorf("record the %s verdict: no link_scan_result row was written", verdict)
		}
		// No ActorUserID: the scanner is the system. audit.Log refuses a nil
		// actor on any other action.
		return audit.Log(ctx, q, audit.Entry{
			TeamID:     target.TeamID,
			Action:     action,
			EntityType: audit.EntityLink,
			EntityID:   target.LinkID,
			Metadata: map[string]any{
				"threat_types":    threats,
				"destination_url": target.URL,
			},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return verdictGone, nil
	case err != nil:
		return "", fmt.Errorf("apply safe browsing verdict: %w", err)
	}

	// The state has committed, so what follows must happen whatever is left of
	// the caller's time: the sweep's budget or the background check's ten
	// seconds may end in the gap. On the caller's context a just-flagged link
	// would keep forwarding from its cached active entry for up to an hour, and
	// a just-cleared one would keep its block page for up to 29 minutes.
	afterCommit, cancel := context.WithTimeout(context.WithoutCancel(ctx), afterCommitTimeout)
	defer cancel()
	switch outcome {
	case verdictFlagged:
		d.confirmThreats(afterCommit, target.LinkID, threats, result.ValidFor)
		d.invalidateLink(afterCommit, hostname, slug)
		d.Log.Error("link flagged by Safe Browsing",
			"link_id", target.LinkID, "team_id", target.TeamID, "threat_types", threats)
	case verdictUnflagged:
		d.clearThreatConfirmation(afterCommit, target.LinkID)
		d.invalidateLink(afterCommit, hostname, slug)
		d.Log.Info("link unflagged by Safe Browsing", "link_id", target.LinkID, "team_id", target.TeamID)
	case verdictConfirmed:
		d.confirmThreats(afterCommit, target.LinkID, threats, result.ValidFor)
	}
	return outcome, nil
}

// confirmationTTL is how long a confirmation may vouch for a block: never
// longer than Google's answer allows, never past the terms' thirty minutes,
// and a minute short of either.
func confirmationTTL(validFor time.Duration) time.Duration {
	return min(validFor, confirmationMaxAge) - confirmationMargin
}

// confirmThreats records a fresh confirmation for a flagged link. When its
// TTL would not be positive nothing is written and any older key is dropped,
// so a key never vouches for longer than the newest answer allows; the next
// redirect then asks Google again. A Redis failure is a Warn: the redirect
// path re-checks when the key is missing, which costs a wait, not a wrong
// answer.
func (d Deps) confirmThreats(ctx context.Context, linkID uuid.UUID, threats []string, validFor time.Duration) {
	if d.Cache == nil {
		return
	}
	ttl := confirmationTTL(validFor)
	if ttl <= 0 || len(threats) == 0 {
		d.clearThreatConfirmation(ctx, linkID)
		return
	}
	if err := d.Cache.ConfirmThreats(ctx, linkID.String(), threats, ttl); err != nil {
		d.Log.Warn("safe browsing confirmation write failed", "error", err, "link_id", linkID)
	}
}

// clearThreatConfirmation drops a link's confirmation, best-effort: a key left
// behind still holds a confirmation younger than thirty minutes, and only a
// flagged link ever reads it.
func (d Deps) clearThreatConfirmation(ctx context.Context, linkID uuid.UUID) {
	if d.Cache == nil {
		return
	}
	if err := d.Cache.ClearThreatConfirmation(ctx, linkID.String()); err != nil {
		d.Log.Warn("safe browsing confirmation delete failed", "error", err, "link_id", linkID)
	}
}

// checkTarget asks the checker about one link's destination, and is the one
// place a single-URL answer is read: the check after a write uses it, and so
// does the redirect path's re-check of a flagged link. A URL missing from the
// answer is errNoVerdict, never a clean result, and no checker at all is
// errScanningOff.
func (d Deps) checkTarget(ctx context.Context, target scanTarget) (scanning.Result, error) {
	if d.Scanner == nil {
		return scanning.Result{}, errScanningOff
	}
	results, err := d.Scanner.Check(ctx, []string{target.URL})
	if err != nil {
		return scanning.Result{}, err
	}
	result, ok := results[target.URL]
	if !ok {
		return scanning.Result{}, errNoVerdict
	}
	return result, nil
}

// scanSoon checks one link in the background right after a write committed:
// after createLink, and after updateLink left an active link on a destination
// no check has judged, whether new or changed while the link was disabled.
// Best-effort by design — Vercel does not promise to run work past the
// response, and the sweep exists for the check that never finishes.
func (d Deps) scanSoon(ctx context.Context, target scanTarget) {
	if d.Scanner == nil {
		return
	}
	d.inBackground(ctx, target.LinkID, func(ctx context.Context) {
		d.scanOne(ctx, target)
	})
}

func (d Deps) scanOne(ctx context.Context, target scanTarget) {
	result, err := d.checkTarget(ctx, target)
	if err != nil {
		d.logCheckFailure(err, "link_id", target.LinkID)
		return
	}
	d.applyAndLog(ctx, target, result)
}

// applyAndLog applies a verdict a background check produced. Nobody waits on
// it, and a failure leaves the link due for the sweep; logApplyFailure says
// how loudly it is reported.
func (d Deps) applyAndLog(ctx context.Context, target scanTarget, result scanning.Result) {
	if _, err := d.applyVerdict(ctx, target, result); err != nil {
		d.logApplyFailure(err, target.LinkID)
	}
}

// logApplyFailure reports a verdict that could not be written. That is the
// database failing, so it is an Error and reaches Sentry, and so is a lock wait
// that outlasted scanLockTimeout. Running out of time or being cancelled is the
// exception and stays a Warn: the background budget is shared with the Google
// call, so a deadline says the check was slow, not that Postgres is broken. The
// sweep's own deadline never gets here, because applyDue stops before it and
// counts the link as remaining. The one place this is decided, for the checks
// after a write and the sweep alike.
func (d Deps) logApplyFailure(err error, linkID uuid.UUID) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		d.Log.Warn("apply safe browsing verdict", "error", err, "link_id", linkID)
		return
	}
	d.Log.Error("apply safe browsing verdict", "error", err, "link_id", linkID)
}

// inBackground runs work on its own goroutine, detached from the request so
// the response does not cancel it, bounded by backgroundScanTimeout. It
// recovers a panic the way HandleDeepHealth's pings do: a bare goroutine has
// no recover anywhere on its stack, and a panic there would take the whole
// process down, the redirect surface included.
func (d Deps) inBackground(ctx context.Context, linkID uuid.UUID, work func(context.Context)) {
	detached := context.WithoutCancel(ctx)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				d.Log.Error("safe browsing background check panicked",
					"error", fmt.Errorf("panic: %v", r), "link_id", linkID)
			}
		}()
		ctx, cancel := context.WithTimeout(detached, backgroundScanTimeout)
		defer cancel()
		work(ctx)
	}()
}

// logCheckFailure logs a failed check at Warn, which never reaches Sentry: a
// failed check changes nothing, and the link stays due. A spent quota is the
// exception. It is an Error, coalesced in cmd/api to one Sentry event an hour,
// because it does not clear by itself and stops every check until it does.
func (d Deps) logCheckFailure(err error, attrs ...any) {
	if scanning.QuotaExceeded(err) {
		d.Log.Error("safe browsing quota exhausted", append([]any{"error", err}, attrs...)...)
		return
	}
	d.Log.Warn("safe browsing check failed", append([]any{"error", err}, attrs...)...)
}
