package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mheob/kurze-url/apps/api/internal/db"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// scanBatchSize is how many due links one sweep takes. 200 links need at most
// 30 expressions each, so at most 6,000 prefixes before de-duplication, which
// at 250 prefixes per request is about 24 hashes.search calls of a few hundred
// milliseconds each (twice that only if every link had a URL whose two
// readings disagree, which lookupExpressions explains), plus 200 short
// transactions of a few round trips each to a database in the same region.
// That is comfortably inside scanBudget in the ordinary case, where most links
// need far fewer than 30. Twice an hour it is 9,600 links a day, several times
// this instance's link count, so the backlog the first sweeps after deploy
// find (every existing link is due) drains within hours.
const scanBatchSize = 200

const (
	// scanBudget bounds the run itself. The workflow's own step failing
	// withholds the heartbeat either way, as for /internal/retention.
	scanBudget = 25 * time.Second
	// scanCountBudget is what the closing count of remaining links gets, from
	// a context the run's budget does not cancel, so a run that used all of
	// its time still reports how much is left.
	scanCountBudget = 5 * time.Second
)

// errScanCheck marks a sweep whose call to Google failed as a whole.
var errScanCheck = errors.New("safe browsing check failed")

// scanReport is POST /internal/scan's body. The workflow's logs read these
// keys.
type scanReport struct {
	// Checked counts links whose verdict was applied, or discarded because the
	// link changed or disappeared while Google was answering.
	Checked   int `json:"checked"`
	Flagged   int `json:"flagged"`
	Unflagged int `json:"unflagged"`
	// Failed counts links that got no verdict, or whose verdict could not be
	// written. They stay due. Links the run's budget did not reach are not
	// failed: they are Remaining.
	Failed int `json:"failed"`
	// Remaining counts links still due after this run, flagged links
	// included: those are due on every sweep, so this does not reach zero
	// while any link is blocked.
	Remaining int64 `json:"remaining"`
}

// HandleScan answers POST /internal/scan: one sweep of due links, the half of
// scanning that does not depend on Vercel letting a goroutine finish. It
// checks links never checked for their current destination, flagged links
// (so a false positive Google corrects is lifted unvisited), and every other
// link once a day.
//
// The authorization is HandleRetention's, deliberately unchanged: the same 404
// for a missing and a wrong token, the same constant-time compare, the same
// empty-means-disabled rule. Like /internal/retention it sits on the root
// router above the hostname split, outside Huma and the OpenAPI document, so
// the token is the security boundary here, not the hostname.
//
// Two ways a run ends early look alike and are not. Running out of its own
// time budget is an ordinary run: the report says what was done, the rest
// stays due, and the answer is 200 so the heartbeat fires — the first sweeps
// after deploy find more due links than one budget holds. Google failing the
// check as a whole is a failed run, a 502 that withholds the heartbeat.
func (d Deps) HandleScan(w http.ResponseWriter, r *http.Request) {
	if d.Config.ScanToken == "" ||
		subtle.ConstantTimeCompare(
			[]byte(r.Header.Get("X-Scan-Token")),
			[]byte(d.Config.ScanToken),
		) != 1 {
		http.NotFound(w, r)
		return
	}

	// 503 rather than a report of nothing: a sweep that cannot check anything
	// must withhold the heartbeat, not tell the monitor all is well.
	if d.Scanner == nil {
		d.Log.Warn("safe browsing sweep requested, but SAFE_BROWSING_API_KEY is unset")
		http.Error(w, "safe browsing scanning is not configured", http.StatusServiceUnavailable)
		return
	}

	report, err := d.sweep(r.Context(), scanBatchSize)
	switch {
	case errors.Is(err, errScanCheck):
		d.logCheckFailure(err, "phase", "sweep")
		http.Error(w, "safe browsing did not answer", http.StatusBadGateway)
		return
	case err != nil:
		d.Log.Error("safe browsing sweep failed", "error", err)
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}

	// Info, not Debug: in the workflow's history this line is what tells
	// "ran and found nothing due" from "did not run".
	d.Log.Info("safe browsing sweep ran",
		"checked", report.Checked, "flagged", report.Flagged, "unflagged", report.Unflagged,
		"failed", report.Failed, "remaining", report.Remaining)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// sweep checks one batch of due links in as few hashes.search calls as the
// prefix limit allows, and applies each verdict until the budget runs out.
// What the budget does not reach stays due for the next run.
//
// The only error it returns for a failed call to Google is errScanCheck, and
// only while its own budget is still running. Judging by the error would not
// do: Google's client gives up after five seconds with an error that is
// context.DeadlineExceeded to errors.Is, exactly as this run's own deadline
// does, and an outage read as "ran out of time" would keep the heartbeat
// green for as long as it lasted. Whose clock ran out is what tells them
// apart, and the budget's own context knows.
func (d Deps) sweep(ctx context.Context, limit int) (scanReport, error) {
	now := d.now()
	length := d.ScanBudget
	if length <= 0 {
		length = scanBudget
	}
	budget, cancel := context.WithTimeout(ctx, length)
	defer cancel()

	// No team_id, and that is correct here. Everywhere else in this codebase
	// a query without a tenancy filter is a data-leak bug; this one acts for
	// the instance rather than for a caller, like the retention job's
	// deletes, and scoping it to a team would make "every link is checked
	// daily" depend on who happened to call. Every write a verdict causes
	// filters by the team_id this returns.
	due, err := d.Queries.ListDueLinksForScan(budget, db.ListDueLinksForScanParams{
		Now: now, BatchLimit: int32(limit),
	})
	if err != nil {
		return scanReport{}, fmt.Errorf("list due links: %w", err)
	}

	var report scanReport
	if len(due) > 0 {
		results, err := d.Scanner.Check(budget, distinctDestinations(due))
		if err != nil && budget.Err() == nil {
			return scanReport{}, fmt.Errorf("%w: %w", errScanCheck, err)
		}
		// With the budget spent there is nothing to apply, and applyDue says
		// so on its first look at it: the run reports zero checked and every
		// link it did not reach as remaining.
		d.applyDue(budget, due, results, &report)
	}

	countCtx, cancelCount := context.WithTimeout(context.WithoutCancel(ctx), scanCountBudget)
	defer cancelCount()
	// No team_id, for the reason given at the list above.
	remaining, err := d.Queries.CountDueLinksForScan(countCtx, now)
	if err != nil {
		return scanReport{}, fmt.Errorf("count due links: %w", err)
	}
	report.Remaining = remaining
	return report, nil
}

// distinctDestinations lists each destination of the batch once, in order. Two
// links may point at the same URL, and a Checker is asked about each URL once.
func distinctDestinations(due []db.ListDueLinksForScanRow) []string {
	urls := make([]string, 0, len(due))
	seen := make(map[string]bool, len(due))
	for _, l := range due {
		if !seen[l.DestinationURL] {
			seen[l.DestinationURL] = true
			urls = append(urls, l.DestinationURL)
		}
	}
	return urls
}

// applyDue applies the batch's verdicts one link at a time, in due order, and
// stops at the first link it finds the budget already spent on. A verdict that
// fails to write counts against Failed and the link stays due, unless the
// budget ran out inside its own transaction: that link was not reached, so it
// is Remaining like the ones behind it.
//
// A link whose destination is missing from the results has no verdict. That is
// a failure, never a clean check: recording one would take it off the
// never-checked list without anyone having judged it.
func (d Deps) applyDue(
	budget context.Context,
	due []db.ListDueLinksForScanRow,
	results map[string]scanning.Result,
	report *scanReport,
) {
	for _, l := range due {
		if budget.Err() != nil {
			return
		}
		result, ok := results[l.DestinationURL]
		if !ok {
			report.Failed++
			d.logCheckFailure(errNoVerdict, "link_id", l.ID)
			continue
		}

		outcome, err := d.applyVerdict(budget, scanTarget{
			LinkID: l.ID, TeamID: l.TeamID, URL: l.DestinationURL,
		}, result)
		switch {
		case err != nil && budget.Err() != nil:
			return
		case err != nil:
			report.Failed++
			d.logApplyFailure(err, l.ID)
			continue
		}

		report.Checked++
		switch outcome {
		case verdictFlagged:
			report.Flagged++
		case verdictUnflagged:
			report.Unflagged++
		}
	}
}
