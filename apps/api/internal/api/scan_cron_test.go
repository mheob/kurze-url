package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

const testCronSecret = "test-cron-secret"

// cronRequest sends what Vercel Cron sends: a GET with the project's
// CRON_SECRET as a bearer token. authorization == "" sends no header at all.
func cronRequest(t *testing.T, handler http.Handler, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/internal/scan", nil)
	req.Host = "api.test"
	req.Header.Set("User-Agent", "vercel-cron/1.0")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// cronFixture is sweepFixture with CRON_SECRET set, so a test can call the
// sweep the way Vercel Cron does.
func cronFixture(t *testing.T) (*fixture, *fakeChecker, string) {
	t.Helper()
	f, checker, destination := sweepFixture(t)
	f.deps.Config.CronSecret = testCronSecret
	return f, checker, destination
}

// heartbeat stands in for the Better Stack heartbeat: it counts pings and
// answers each with status.
type heartbeat struct {
	pings  atomic.Int32
	server *httptest.Server
}

func newHeartbeat(t *testing.T, status int) *heartbeat {
	t.Helper()
	h := &heartbeat{}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h.pings.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(h.server.Close)
	return h
}

func TestCronRunsTheSweepWithTheCronSecret(t *testing.T) {
	f, checker, destination := cronFixture(t)
	checker.pass(destination)

	rec := cronRequest(t, api.NewRouter(f.deps), "Bearer "+testCronSecret)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, 1, decodeScan(t, rec).Checked)
	require.NotNil(t, scanCheckedAt(t, f.pool, f.linkID))
}

// Vercel calls the production deployment, and which of the project's
// hostnames that request carries is not documented. The sweep sits above the
// hostname split, so the shared short domain reaches it as well as the API's.
func TestCronReachesTheSweepOnTheShortDomain(t *testing.T) {
	f, _, _ := cronFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/internal/scan", nil)
	req.Host = "go.test"
	req.Header.Set("Authorization", "Bearer "+testCronSecret)
	rec := httptest.NewRecorder()

	api.NewRouter(f.deps).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

func TestCronRefusesAWrongSecret(t *testing.T) {
	f, _, _ := cronFixture(t)
	router := api.NewRouter(f.deps)

	require.Equal(t, http.StatusNotFound, cronRequest(t, router, "").Code)
	require.Equal(t, http.StatusNotFound, cronRequest(t, router, "Bearer wrong").Code)
	require.Equal(t, http.StatusNotFound, cronRequest(t, router, testCronSecret).Code,
		"the secret without its Bearer prefix is not what Vercel sends")
	require.Nil(t, scanCheckedAt(t, f.pool, f.linkID))
}

// An unset CRON_SECRET disables the bearer path rather than opening it: an
// empty secret must not match an empty bearer token.
func TestCronIsDisabledWhenNoSecretIsConfigured(t *testing.T) {
	f, _, _ := cronFixture(t)
	f.deps.Config.CronSecret = ""
	router := api.NewRouter(f.deps)

	require.Equal(t, http.StatusNotFound, cronRequest(t, router, "Bearer ").Code)
	require.Equal(t, http.StatusNotFound, cronRequest(t, router, "Bearer").Code)
	require.Equal(t, http.StatusNotFound, cronRequest(t, router, "Bearer anything").Code)
}

// Each secret counts only in its own header, so one leaked value authorizes
// one caller's way in and not the other's.
func TestCronSecretAndScanTokenDoNotStandInForEachOther(t *testing.T) {
	f, _, _ := cronFixture(t)
	router := api.NewRouter(f.deps)

	require.Equal(t, http.StatusNotFound, scanRequest(t, router, testCronSecret).Code)
	require.Equal(t, http.StatusNotFound, cronRequest(t, router, "Bearer "+testScanToken).Code)
}

// Both callers run the same sweep, and a successful one pings the heartbeat
// whoever asked for it: the scheduled run and a manual run from the workflow.
func TestScanPingsTheHeartbeatAfterASuccessfulSweep(t *testing.T) {
	for name, send := range map[string]func(*testing.T, http.Handler) *httptest.ResponseRecorder{
		"vercel cron": func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
			return cronRequest(t, h, "Bearer "+testCronSecret)
		},
		"manual run": func(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
			return scanRequest(t, h, testScanToken)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, checker, destination := cronFixture(t)
			checker.pass(destination)
			beat := newHeartbeat(t, http.StatusOK)
			f.deps.Config.ScanHeartbeatURL = beat.server.URL

			rec := send(t, api.NewRouter(f.deps))

			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
			require.Equal(t, int32(1), beat.pings.Load())
		})
	}
}

// Running out of its own budget is an ordinary run, so it pings as well: the
// first sweeps after a deploy find more due links than one budget holds.
func TestScanPingsTheHeartbeatWhenItStopsAtItsBudget(t *testing.T) {
	f, checker, destination := cronFixture(t)
	threeDueLinks(t, f, checker, destination)
	f.deps.ScanBudget = testScanBudget
	checker.onCheck(untilDone)
	checker.fail(context.DeadlineExceeded)
	beat := newHeartbeat(t, http.StatusOK)
	f.deps.Config.ScanHeartbeatURL = beat.server.URL

	rec := cronRequest(t, api.NewRouter(f.deps), "Bearer "+testCronSecret)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, int32(1), beat.pings.Load())
}

// Every answer but a sweep's 200 withholds the ping, so the heartbeat going
// missing is what reports the failure.
func TestScanWithholdsTheHeartbeatFromAFailedSweep(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare       func(*fixture, *fakeChecker)
		authorization string
		status        int
	}{
		"google fails": {
			prepare:       func(_ *fixture, c *fakeChecker) { c.fail(errors.New("connection reset")) },
			authorization: "Bearer " + testCronSecret,
			status:        http.StatusBadGateway,
		},
		"no scanner": {
			prepare:       func(f *fixture, _ *fakeChecker) { f.deps.Scanner = nil },
			authorization: "Bearer " + testCronSecret,
			status:        http.StatusServiceUnavailable,
		},
		"wrong secret": {
			prepare:       func(*fixture, *fakeChecker) {},
			authorization: "Bearer wrong",
			status:        http.StatusNotFound,
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, checker, _ := cronFixture(t)
			tc.prepare(f, checker)
			beat := newHeartbeat(t, http.StatusOK)
			f.deps.Config.ScanHeartbeatURL = beat.server.URL

			rec := cronRequest(t, api.NewRouter(f.deps), tc.authorization)

			require.Equal(t, tc.status, rec.Code, "body: %s", rec.Body.String())
			require.Zero(t, beat.pings.Load())
		})
	}
}

// The sweep did its work, so it still answers 200; the heartbeat going
// missing is the alarm, and the log says why.
func TestScanAnswers200WhenTheHeartbeatRefusesThePing(t *testing.T) {
	f, checker, destination := cronFixture(t)
	checker.pass(destination)
	logs := captureLogs(f)
	beat := newHeartbeat(t, http.StatusInternalServerError)
	f.deps.Config.ScanHeartbeatURL = beat.server.URL

	rec := cronRequest(t, api.NewRouter(f.deps), "Bearer "+testCronSecret)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, int32(1), beat.pings.Load())
	require.Contains(t, logs.String(), `level=WARN msg="safe browsing sweep could not ping its heartbeat"`)
	require.Contains(t, logs.String(), "status=500")
}

// A heartbeat URL is a credential: whoever holds it can keep the monitor
// green. Go prints a request's URL into its transport errors, so the log
// carries the cause without it.
func TestScanKeepsTheHeartbeatURLOutOfItsLogs(t *testing.T) {
	f, checker, destination := cronFixture(t)
	checker.pass(destination)
	logs := captureLogs(f)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	f.deps.Config.ScanHeartbeatURL = closed.URL + "/api/v1/heartbeat/secret-heartbeat-token"

	rec := cronRequest(t, api.NewRouter(f.deps), "Bearer "+testCronSecret)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, logs.String(), `level=WARN msg="safe browsing sweep could not ping its heartbeat"`)
	require.False(t, strings.Contains(logs.String(), "secret-heartbeat-token"),
		"logs: %s", logs.String())
}

// No heartbeat configured is a valid setup (local development, Preview): the
// sweep runs and pings nothing.
func TestScanRunsWithoutAHeartbeatURL(t *testing.T) {
	f, checker, destination := cronFixture(t)
	checker.pass(destination)
	logs := captureLogs(f)

	rec := cronRequest(t, api.NewRouter(f.deps), "Bearer "+testCronSecret)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.NotContains(t, logs.String(), "heartbeat")
}
