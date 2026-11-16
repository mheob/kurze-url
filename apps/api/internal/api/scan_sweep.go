package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	// scanBudget bounds the run itself. A run that fails withholds the
	// heartbeat either way, as for /internal/retention.
	scanBudget = 25 * time.Second
	// scanCountBudget is what the closing count of remaining links gets, from
	// a context the run's budget does not cancel, so a run that used all of
	// its time still reports how much is left.
	scanCountBudget = 5 * time.Second
	// scanHeartbeatTimeout bounds the ping after a successful run. Vercel
	// freezes a Go function once its response is done, so the ping happens
	// inside the request, before the answer, and a slow Better Stack delays
	// the answer by at most this much.
	scanHeartbeatTimeout = 5 * time.Second
)

// errScanCheck marks a sweep whose call to Google failed as a whole.
var errScanCheck = errors.New("safe browsing check failed")

// scanReport is /internal/scan's body. The function logs of Vercel's cron
// invocations, and a manual run's workflow log, read these keys.
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

	// stoppedAtBudget says the run's own budget ended it before it had been
	// through its batch. Not in the body, which answers 200 either way;
	// HandleScan logs it.
	stoppedAtBudget bool
}

// HandleScan answers /internal/scan: one sweep of due links, the half of
// scanning that does not depend on a request to a link. It checks links never
// checked for their current destination, flagged links (so a false positive
// Google corrects is lifted unvisited), and every other link once a day.
//
// Two callers reach it, each with its own secret in its own header (see
// scanAuthorized): Vercel Cron, at :07 and :37 every hour from
// apps/api/vercel.json, with a GET carrying CRON_SECRET as a bearer token;
// and .github/workflows/scan.yml, run by hand, with a POST carrying
// X-Scan-Token. Both get the same 404 for a missing and a wrong secret, the
// same constant-time compare and the same empty-means-closed rule as
// HandleRetention. Like /internal/retention it sits on the root router above
// the hostname split, outside Huma and the OpenAPI document, so the secrets
// are the security boundary here, not the hostname. A GET that writes is
// unusual; it is there because Vercel Cron sends nothing but GET.
//
// A successful run pings SCAN_HEARTBEAT_URL before it answers, whichever
// caller asked, and nothing else does. That makes the heartbeat a
// dead-man's switch for both hazards at once: a failing sweep withholds the
// ping, and so does a schedule that stopped firing.
//
// Two ways a run ends early look alike and are not. Running out of its own
// time budget is an ordinary run: the report says what was done, the rest
// stays due, and the answer is 200 so the heartbeat fires — the first sweeps
// after deploy find more due links than one budget holds. It is logged at
// Warn all the same, with the counts: a run that keeps stopping there with
// nothing checked is Google answering too slowly for the batch, a stall the
// green heartbeat alone would hide. Google failing the check as a whole is a
// failed run, a 502 that withholds the heartbeat.
//
// Vercel Cron may deliver one scheduled run twice. Two sweeps at once check
// the same links twice and write nothing twice: applyVerdict locks each link
// and records a verdict only when it changes.
func (d Deps) HandleScan(w http.ResponseWriter, r *http.Request) {
	if !d.scanAuthorized(r) {
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

	counts := []any{
		"checked", report.Checked, "flagged", report.Flagged, "unflagged", report.Unflagged,
		"failed", report.Failed, "remaining", report.Remaining,
	}
	// Info, not Debug: in the function logs this line is what tells "ran
	// and found nothing due" from "did not run".
	d.Log.Info("safe browsing sweep ran", counts...)
	if report.stoppedAtBudget {
		d.Log.Warn("safe browsing sweep stopped at its budget", counts...)
	}

	d.pingScanHeartbeat(r.Context())

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// scanAuthorized reports whether the request carries either caller's secret
// in that caller's own header: Vercel Cron's "Bearer <CRON_SECRET>" in
// Authorization, or SCAN_TOKEN in X-Scan-Token. An unset secret closes its
// way in, and a secret in the other caller's header counts for nothing, so
// one leaked value authorizes one caller.
func (d Deps) scanAuthorized(r *http.Request) bool {
	if d.Config.CronSecret != "" &&
		secretMatches(r.Header.Get("Authorization"), "Bearer "+d.Config.CronSecret) {
		return true
	}
	return d.Config.ScanToken != "" &&
		secretMatches(r.Header.Get("X-Scan-Token"), d.Config.ScanToken)
}

// secretMatches compares in constant time, so the answer's timing says
// nothing about how much of a guess was right.
func secretMatches(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// pingScanHeartbeat tells the Better Stack heartbeat that a sweep succeeded.
// A failed ping is logged at Warn and changes nothing else: the sweep did its
// work, and the heartbeat going missing is the alarm. The URL is a credential
// — whoever holds it can keep the monitor green — and Go prints a request's
// URL into its transport errors, so only the cause is logged.
//
// The context is detached from the caller's: a client that hangs up after the
// sweep must not cost the run its ping.
func (d Deps) pingScanHeartbeat(ctx context.Context) {
	if d.Config.ScanHeartbeatURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scanHeartbeatTimeout)
	defer cancel()

	const message = "safe browsing sweep could not ping its heartbeat"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.Config.ScanHeartbeatURL, nil)
	if err != nil {
		d.Log.Warn(message, "error", "SCAN_HEARTBEAT_URL is not a valid URL")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		d.Log.Warn(message, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		d.Log.Warn(message, "status", resp.StatusCode)
	}
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
		report.stoppedAtBudget = d.applyDue(budget, due, results, &report)
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
// stops at the first link it finds the budget already spent on, reporting
// whether it did. A verdict that fails to write counts against Failed and the
// link stays due, unless the budget ran out inside its own transaction: that
// link was not reached, so it is Remaining like the ones behind it.
//
// A link whose destination is missing from the results has no verdict. That is
// a failure, never a clean check: recording one would take it off the
// never-checked list without anyone having judged it.
func (d Deps) applyDue(
	budget context.Context,
	due []db.ListDueLinksForScanRow,
	results map[string]scanning.Result,
	report *scanReport,
) (stopped bool) {
	for _, l := range due {
		if budget.Err() != nil {
			return true
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
			return true
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
	return false
}
