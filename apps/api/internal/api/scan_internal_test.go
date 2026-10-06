package api

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// min(ValidFor, thirty minutes) minus a minute. Not positive means no key at
// all, so a verdict Google says may not be relied on never blocks a second
// redirect.
func TestConfirmationTTLStaysInsideThirtyMinutes(t *testing.T) {
	for _, tc := range []struct{ validFor, want time.Duration }{
		{5 * time.Minute, 4 * time.Minute},
		{30 * time.Minute, 29 * time.Minute},
		{2 * time.Hour, 29 * time.Minute},
		{time.Minute, 0},
		{0, -time.Minute},
	} {
		require.Equal(t, tc.want, confirmationTTL(tc.validFor), "validFor %s", tc.validFor)
	}
}

// The bounds nest: a lock wait must end well inside every budget a verdict is
// applied under, and the work after the commit well inside the minute a
// confirmation's TTL is shortened by, since the TTL counts from the SET.
func TestTheScanTimeoutsNestInsideEachOther(t *testing.T) {
	require.Less(t, scanLockTimeout, backgroundScanTimeout)
	require.Less(t, scanLockTimeout, scanBudget)
	require.Less(t, afterCommitTimeout, confirmationMargin)
}

// checkerFunc adapts a function to scanning.Checker, for the tests below that
// need no database. The database-backed tests use api_test's fakeChecker.
type checkerFunc func(ctx context.Context, urls []string) (map[string]scanning.Result, error)

func (f checkerFunc) Check(ctx context.Context, urls []string) (map[string]scanning.Result, error) {
	return f(ctx, urls)
}

// checkTarget is the one place a single-URL answer is read, so it is where
// "missing from the map" has to become an error rather than a clean result.
func TestCheckTargetAsksForTheOneURLAndNeverReadsSilenceAsClean(t *testing.T) {
	ctx := context.Background()
	target := scanTarget{LinkID: uuid.New(), TeamID: uuid.New(), URL: "https://example.org/asked"}
	var asked []string
	answer := map[string]scanning.Result{}
	d := Deps{Scanner: checkerFunc(func(_ context.Context, urls []string) (map[string]scanning.Result, error) {
		asked = urls
		return answer, nil
	})}

	answer["https://example.org/somebody-else"] = scanning.Result{ValidFor: time.Minute}
	_, err := d.checkTarget(ctx, target)
	require.ErrorIs(t, err, errNoVerdict, "an answer about another URL is no answer about this one")
	require.Equal(t, []string{target.URL}, asked)

	answer[target.URL] = scanning.Result{ThreatTypes: []string{"MALWARE"}, ValidFor: time.Minute}
	result, err := d.checkTarget(ctx, target)
	require.NoError(t, err)
	require.Equal(t, answer[target.URL], result)
}

func TestCheckTargetPassesAFailureThroughAndRefusesWithoutAScanner(t *testing.T) {
	ctx := context.Background()
	target := scanTarget{LinkID: uuid.New(), TeamID: uuid.New(), URL: "https://example.org/asked"}

	failing := Deps{Scanner: checkerFunc(func(context.Context, []string) (map[string]scanning.Result, error) {
		return nil, fmt.Errorf("search: %w", scanning.ErrQuotaExceeded)
	})}
	_, err := failing.checkTarget(ctx, target)
	require.True(t, scanning.QuotaExceeded(err), "the quota must stay recognisable for the coalescing rule")

	_, err = Deps{}.checkTarget(ctx, target)
	require.ErrorIs(t, err, errScanningOff)
}

// recordingHandler hands every log record to a channel, so a test can wait for
// a line written on another goroutine without racing on a shared buffer.
type recordingHandler struct{ records chan slog.Record }

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records <- r
	return nil
}

func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

func nextRecord(t *testing.T, records <-chan slog.Record) slog.Record {
	t.Helper()
	select {
	case r := <-records:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was logged")
		return slog.Record{}
	}
}

// Only Error reaches Sentry. A failed check changes nothing and the link stays
// due, so it is a Warn; a spent quota stops every check until it clears.
func TestLogCheckFailureRaisesOnlyASpentQuotaToError(t *testing.T) {
	for _, tc := range []struct {
		err     error
		level   slog.Level
		message string
	}{
		{fmt.Errorf("search: %w", scanning.ErrQuotaExceeded), slog.LevelError, "safe browsing quota exhausted"},
		{errNoVerdict, slog.LevelWarn, "safe browsing check failed"},
		{context.DeadlineExceeded, slog.LevelWarn, "safe browsing check failed"},
	} {
		records := make(chan slog.Record, 1)
		Deps{Log: slog.New(recordingHandler{records})}.logCheckFailure(tc.err, "link_id", uuid.New())

		record := nextRecord(t, records)
		require.Equal(t, tc.level, record.Level, "error %v", tc.err)
		require.Equal(t, tc.message, record.Message, "error %v", tc.err)
	}
}

// The response has gone out and its context is cancelled; the check must not
// be, and it must still end on its own.
func TestInBackgroundOutlivesTheRequestButNotItsOwnTimeout(t *testing.T) {
	request, cancel := context.WithCancel(context.Background())
	cancel()

	type seen struct {
		err      error
		deadline time.Time
		bounded  bool
	}
	got := make(chan seen, 1)
	started := time.Now()
	Deps{Log: slog.New(slog.DiscardHandler)}.inBackground(request, uuid.New(), func(ctx context.Context) {
		deadline, bounded := ctx.Deadline()
		got <- seen{err: ctx.Err(), deadline: deadline, bounded: bounded}
	})

	select {
	case s := <-got:
		require.NoError(t, s.err, "the request's cancellation must not reach the check")
		require.True(t, s.bounded)
		require.WithinDuration(t, started.Add(backgroundScanTimeout), s.deadline, time.Second)
	case <-time.After(5 * time.Second):
		t.Fatal("the background work never ran")
	}
}

// A bare goroutine has no recover on its stack, so an unrecovered panic there
// would take the whole process down, the redirect surface included. Reaching
// the assertion at all is half of what this test checks.
func TestInBackgroundRecoversAPanicAndReportsIt(t *testing.T) {
	records := make(chan slog.Record, 1)
	Deps{Log: slog.New(recordingHandler{records})}.inBackground(context.Background(), uuid.New(),
		func(context.Context) { panic("boom") })

	record := nextRecord(t, records)
	require.Equal(t, slog.LevelError, record.Level)
	require.Equal(t, "safe browsing background check panicked", record.Message)
	var reported error
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "error" {
			reported, _ = a.Value.Any().(error)
		}
		return true
	})
	require.EqualError(t, reported, "panic: boom")
}

// unreachablePool is a pool whose every connection attempt is refused, which
// is the database failing in a way no context has anything to do with.
func unreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://nobody:nothing@127.0.0.1:1/none?connect_timeout=2")
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// A verdict that cannot be written is the database failing, and that has to
// reach Sentry. Running out of the background budget is not: it is shared with
// the Google call, so a deadline says the check was slow, not that Postgres is
// broken.
func TestApplyAndLogRaisesADatabaseFailureButNotATimeout(t *testing.T) {
	target := scanTarget{LinkID: uuid.New(), TeamID: uuid.New(), URL: "https://example.org/asked"}
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name  string
		ctx   context.Context
		level slog.Level
	}{
		{"database refused", context.Background(), slog.LevelError},
		{"budget spent", expired, slog.LevelWarn},
		{"cancelled", cancelled, slog.LevelWarn},
	} {
		records := make(chan slog.Record, 1)
		d := Deps{Pool: unreachablePool(t), Log: slog.New(recordingHandler{records})}

		d.applyAndLog(tc.ctx, target, scanning.Result{ThreatTypes: []string{"MALWARE"}, ValidFor: time.Minute})

		record := nextRecord(t, records)
		require.Equal(t, tc.level, record.Level, tc.name)
		require.Equal(t, "apply safe browsing verdict", record.Message, tc.name)
	}
}
