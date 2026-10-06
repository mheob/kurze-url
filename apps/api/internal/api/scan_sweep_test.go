package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

const testScanToken = "test-scan-token"

// testScanBudget is the sweep's time budget in the tests that run it out.
// Short enough to keep the suite quick, long enough that the due-links query
// ahead of the check is nowhere near it, even under -race on a busy machine.
const testScanBudget = 1500 * time.Millisecond

// scanRequest sends one POST /internal/scan. token == "" sends no header at
// all, which is a different case from sending a wrong one.
func scanRequest(t *testing.T, handler http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/scan", nil)
	req.Host = "api.test"
	if token != "" {
		req.Header.Set("X-Scan-Token", token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// scanResponse is the endpoint's body, spelled out rather than imported: the
// workflow's logs read these keys, so a renamed one has to fail here.
type scanResponse struct {
	Checked   int   `json:"checked"`
	Flagged   int   `json:"flagged"`
	Unflagged int   `json:"unflagged"`
	Failed    int   `json:"failed"`
	Remaining int64 `json:"remaining"`
}

func decodeScan(t *testing.T, rec *httptest.ResponseRecorder) scanResponse {
	t.Helper()
	var body scanResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())
	return body
}

// sweepFixture is newFixture with a destination no other test uses, a fake
// checker that knows only what a test tells it, and a creation date in 2001.
//
// The sweep is instance-wide, and `go test ./...` runs other packages in
// parallel processes that commit due links of their own. Three things keep
// these tests exact anyway. The fake answers only for this test's own URLs,
// so every other link gets no verdict and nothing is written to it. The date
// puts this link at the head of the batch among never-checked links, before
// every link any test commits (internal/db's scan tests seed the year 2000,
// but inside transactions nobody else can see). And Failed, the one count the
// other links land in, is never asserted exactly here unless the batch limit
// keeps them out.
func sweepFixture(t *testing.T, opts ...func(*linkOptions)) (*fixture, *fakeChecker, string) {
	t.Helper()
	destination := uniqueDestination("sweep")
	f := newFixture(t, append([]func(*linkOptions){withDestination(destination)}, opts...)...)
	// A run interrupted before its cleanup leaves its 2001 links behind, and
	// they would sort ahead of this test's own. Nothing else commits a link
	// created before 2002 (internal/db's scan tests seed 2000 inside
	// transactions they roll back), so this deletes only such leftovers.
	_, err := f.pool.Exec(context.Background(),
		`delete from link where created_at < '2002-01-01T00:00:00Z'`)
	require.NoError(t, err)
	_, err = f.pool.Exec(context.Background(),
		`update link set created_at = '2001-01-01T00:00:00Z' where id = $1`, f.linkID)
	require.NoError(t, err)

	checker := newFakeChecker()
	f.deps.Scanner = checker
	f.deps.Config.ScanToken = testScanToken
	return f, checker, destination
}

// extraLink adds a due link beside the fixture's, on the same domain and team.
func extraLink(t *testing.T, f *fixture, destination, createdAt string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`insert into link (domain_id, team_id, slug, destination_url, created_by, created_at)
		 select domain_id, team_id, $2, $3, created_by, $4::timestamptz from link where id = $1
		 returning id`,
		f.linkID, "extra-"+uuid.NewString()[:8], destination, createdAt).Scan(&id))
	return id
}

// threeDueLinks is the fixture's link and two more, oldest first, each with a
// clean verdict waiting for it at the fake.
func threeDueLinks(t *testing.T, f *fixture, checker *fakeChecker, destination string) (second, third uuid.UUID) {
	t.Helper()
	secondURL, thirdURL := uniqueDestination("sweep"), uniqueDestination("sweep")
	second = extraLink(t, f, secondURL, "2001-01-02T00:00:00Z")
	third = extraLink(t, f, thirdURL, "2001-01-03T00:00:00Z")
	for _, url := range []string{destination, secondURL, thirdURL} {
		checker.pass(url)
	}
	return second, third
}

// requireNoneChecked fails when any of the links has a recorded check.
func requireNoneChecked(t *testing.T, f *fixture, ids ...uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		require.Nil(t, scanCheckedAt(t, f.pool, id), "link %s was checked", id)
	}
}

// untilDone blocks until the check's own context ends, the way a slow Google
// does.
func untilDone(ctx context.Context) { <-ctx.Done() }

// cancelOnLog cancels a context the moment a record with the given message is
// logged, which is how a test stops a sweep partway through a batch: the link
// is flagged, committed and logged before the next one is looked at.
type cancelOnLog struct {
	slog.Handler
	message string
	cancel  context.CancelFunc
}

func (h cancelOnLog) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == h.message {
		h.cancel()
	}
	return h.Handler.Handle(ctx, record)
}

func TestScanRefusesWithoutTheToken(t *testing.T) {
	f, _, _ := sweepFixture(t)
	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "").Code)
}

func TestScanRefusesAWrongToken(t *testing.T) {
	f, _, _ := sweepFixture(t)
	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "wrong").Code)
}

// An unset token disables the endpoint rather than opening it.
func TestScanIsDisabledWhenNoTokenIsConfigured(t *testing.T) {
	f, _, _ := sweepFixture(t)
	f.deps.Config.ScanToken = ""

	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "").Code)
	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "anything").Code)
}

// A 503, so the workflow's heartbeat goes missing instead of reporting a scan
// that never ran. The token is still checked first: a caller without it learns
// nothing about how the instance is configured.
func TestScanAnswers503WithoutAScanner(t *testing.T) {
	f, _, _ := sweepFixture(t)
	f.deps.Scanner = nil

	require.Equal(t, http.StatusServiceUnavailable,
		scanRequest(t, api.NewRouter(f.deps), testScanToken).Code)
	require.Equal(t, http.StatusNotFound, scanRequest(t, api.NewRouter(f.deps), "wrong").Code)
}

func TestScanFlagsADueLinkGoogleReports(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	checker.flag(destination, "SOCIAL_ENGINEERING")

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	report := decodeScan(t, rec)
	require.Equal(t, 1, report.Checked, "only this test's destination gets a verdict")
	require.Equal(t, 1, report.Flagged)
	require.Zero(t, report.Unflagged)
	require.GreaterOrEqual(t, report.Remaining, int64(1), "a flagged link is due on every sweep")

	require.Equal(t, "flagged", linkState(t, f.pool, f.linkID))
	entries := auditRows(t, f.pool, f.linkID, "link.flagged")
	require.Len(t, entries, 1)
	require.Nil(t, entries[0].actor)
}

// A false positive Google corrects is lifted even if nobody visits the link.
func TestScanUnflagsALinkGoogleNoLongerReports(t *testing.T) {
	f, checker, destination := sweepFixture(t, withState("flagged"))
	checker.pass(destination)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	report := decodeScan(t, rec)
	require.Equal(t, 1, report.Unflagged)
	require.Zero(t, report.Flagged)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Len(t, auditRows(t, f.pool, f.linkID, "link.unflagged"), 1)
}

// Review focus: a batch takes no more than its limit, oldest first, and what
// it leaves is still due.
func TestScanReportsTheBatchLimitAndWhatRemains(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	second, third := threeDueLinks(t, f, checker, destination)

	report, err := f.deps.SweepForTest(context.Background(), 2)
	require.NoError(t, err)

	require.False(t, report.StoppedAtBudget(), "a batch the run got through did not stop at the budget")
	require.Equal(t, 2, report.Checked)
	require.Zero(t, report.Failed, "a batch of two holds exactly this test's two oldest links")
	require.NotNil(t, scanCheckedAt(t, f.pool, f.linkID))
	require.NotNil(t, scanCheckedAt(t, f.pool, second))
	require.Nil(t, scanCheckedAt(t, f.pool, third), "the third waits for the next sweep")
	require.GreaterOrEqual(t, report.Remaining, int64(1))
}

// Review focus: the first sweeps after deploy find every existing link due, a
// backlog larger than one run's budget. A run that runs out of time is not a
// failed run: it answers 200 so the heartbeat fires, what it did not reach
// stays due and counts as remaining rather than failed. Here the budget runs
// out while Google is still answering, and Google's side of it is the error a
// deadline produces — the very error a 502 would be made of if the sweep
// judged by the error and not by whose clock ran out.
func TestScanStopsAtItsBudgetWhileGoogleIsStillAnswering(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	second, third := threeDueLinks(t, f, checker, destination)
	f.deps.ScanBudget = testScanBudget
	logs := captureLogs(f)
	checker.onCheck(untilDone)
	checker.fail(context.DeadlineExceeded)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	report := decodeScan(t, rec)
	require.Zero(t, report.Checked)
	require.Zero(t, report.Failed, "links the budget did not reach are remaining, not failed")
	require.GreaterOrEqual(t, report.Remaining, int64(3),
		"the count still runs after the budget is spent")
	requireNoneChecked(t, f, f.linkID, second, third)

	// 200 keeps the heartbeat green, so the log is the only place a sweep that
	// keeps stopping here with nothing checked shows up.
	require.Contains(t, logs.String(), `level=WARN msg="safe browsing sweep stopped at its budget"`)
	for _, key := range []string{"checked=0", "flagged=0", "unflagged=0", "failed=0", "remaining="} {
		require.Contains(t, logs.String(), key)
	}
}

// The other half of the same budget: Google answered, but too late to write
// any of it. Nothing is applied, nothing is called failed, and the answer is
// 200.
func TestScanStopsAtItsBudgetBeforeApplyingTheAnswers(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	second, third := threeDueLinks(t, f, checker, destination)
	f.deps.ScanBudget = testScanBudget
	checker.onCheck(untilDone)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	report := decodeScan(t, rec)
	require.Zero(t, report.Checked)
	require.Zero(t, report.Failed)
	require.GreaterOrEqual(t, report.Remaining, int64(3))
	requireNoneChecked(t, f, f.linkID, second, third)
}

// A run that gets partway keeps what it did: the first link is flagged and
// committed before the budget ends, the second and third wait, and none of it
// is an error.
func TestSweepKeepsWhatItAppliedBeforeItsBudgetRanOut(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	second, third := threeDueLinks(t, f, checker, destination)
	checker.flag(destination, "MALWARE")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.deps.Log = slog.New(cancelOnLog{
		Handler: slog.NewTextHandler(io.Discard, nil),
		message: "link flagged by Safe Browsing",
		cancel:  cancel,
	})

	report, err := f.deps.SweepForTest(ctx, 3)
	require.NoError(t, err)

	require.True(t, report.StoppedAtBudget())
	require.Equal(t, 1, report.Checked)
	require.Equal(t, 1, report.Flagged)
	require.Zero(t, report.Failed)
	require.GreaterOrEqual(t, report.Remaining, int64(3), "the flagged link is still due, and so are the two behind it")
	require.Equal(t, "flagged", linkState(t, f.pool, f.linkID))
	requireNoneChecked(t, f, second, third)
}

// Google failing as a whole is a failed run: 502, nothing written, and the
// workflow withholds its heartbeat.
func TestScanAnswers502WhenGoogleFailsAndChangesNothing(t *testing.T) {
	f, checker, _ := sweepFixture(t)
	checker.fail(errors.New("connection reset"))

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
}

// Google's own client gives up after five seconds with an error that satisfies
// errors.Is(err, context.DeadlineExceeded). While the sweep's budget is still
// running that is Google not answering, a 502 that withholds the heartbeat; an
// outage that read as "ran out of time" would keep the heartbeat green for as
// long as it lasted.
func TestScanAnswers502WhenGooglesOwnTimeoutFiresInsideTheBudget(t *testing.T) {
	f, checker, _ := sweepFixture(t)
	checker.fail(fmt.Errorf("hashes.search: %w", context.DeadlineExceeded))

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
}

func TestScanLogsASpentQuotaAtErrorLevel(t *testing.T) {
	f, checker, _ := sweepFixture(t)
	logs := captureLogs(f)
	checker.fail(fmt.Errorf("%w: hashes.search answered 429", scanning.ErrQuotaExceeded))

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, logs.String(), `level=ERROR msg="safe browsing quota exhausted"`)
}

// Any other failed check is a Warn, which never reaches Sentry: it changes
// nothing, and the next sweep asks again.
func TestScanLogsAFailedCheckAtWarnLevel(t *testing.T) {
	f, checker, _ := sweepFixture(t)
	logs := captureLogs(f)
	checker.fail(errors.New("connection reset"))

	scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Contains(t, logs.String(), `level=WARN msg="safe browsing check failed"`)
	require.NotContains(t, logs.String(), "level=ERROR")
}

// A verdict that cannot be written is the database failing, which is an Error
// and reaches Sentry, where a failed check would only be a Warn. The link
// keeps no record of the check, so it stays due, and the sweep still answers
// 200: one failing write is not a failed run.
func TestScanLogsAVerdictItCouldNotWriteAtErrorLevel(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	logs := captureLogs(f)
	checker.flag(destination, "MALWARE")
	closed := testPool(t)
	closed.Close()
	f.deps.Pool = closed

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.GreaterOrEqual(t, decodeScan(t, rec).Failed, 1)
	require.Contains(t, logs.String(), `level=ERROR msg="apply safe browsing verdict"`)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
}

// Review focus: a PATCH lands while Google is answering for the old URL. The
// old URL's verdict must not stick to the new one.
func TestScanDiscardsAVerdictForADestinationChangedMidCheck(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	checker.flag(destination, "SOCIAL_ENGINEERING")
	checker.onCheck(func(ctx context.Context) {
		_, _ = f.pool.Exec(ctx,
			`update link set destination_url = 'https://example.org/replaced' where id = $1`, f.linkID)
	})

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Zero(t, decodeScan(t, rec).Flagged)
	require.Equal(t, "active", linkState(t, f.pool, f.linkID))
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID), "the link stays due for its new destination")
}

// No verdict is not a clean verdict, and the link is named in the log so a
// destination that never gets one can be found.
func TestScanLeavesALinkWithoutAVerdictAlone(t *testing.T) {
	f, _, _ := sweepFixture(t)
	logs := captureLogs(f)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.GreaterOrEqual(t, decodeScan(t, rec).Failed, 1)
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
	require.Contains(t, logs.String(), `level=WARN msg="safe browsing check failed"`)
	require.Contains(t, logs.String(), "link_id="+f.linkID.String())
}

// In the workflow's history this line is what tells a sweep that ran and found
// nothing due from one that did not run, so it carries every count the
// response does.
func TestScanLogsWhatItRanAtInfoLevel(t *testing.T) {
	f, checker, destination := sweepFixture(t)
	logs := captureLogs(f)
	checker.pass(destination)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, logs.String(), `level=INFO msg="safe browsing sweep ran"`)
	for _, key := range []string{"checked=1", "flagged=0", "unflagged=0", "failed=", "remaining="} {
		require.Contains(t, logs.String(), key)
	}
	require.NotContains(t, logs.String(), "stopped at its budget")
}

// The workflow's logs are read by key; decoding into a map is what notices a
// renamed one, which the struct decode above would leave at its zero value.
func TestScanReportsUnderStableKeys(t *testing.T) {
	f, _, _ := sweepFixture(t)

	rec := scanRequest(t, api.NewRouter(f.deps), testScanToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	require.ElementsMatch(t, []string{"checked", "flagged", "unflagged", "failed", "remaining"}, keys)
}
