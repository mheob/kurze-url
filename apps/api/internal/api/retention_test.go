package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

const testRetentionToken = "test-retention-token"

// retention sends one POST /internal/retention. token == "" sends no header at
// all, which is a different case from sending a wrong one.
func retention(t *testing.T, handler http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/retention", nil)
	req.Host = "api.test"
	if token != "" {
		req.Header.Set("X-Retention-Token", token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// retentionReport is the endpoint's response body, spelled out here rather
// than imported: the workflow's logs read these keys, so the test has to
// notice if one of them is renamed.
type retentionReport struct {
	Deleted         int64  `json:"deleted"`
	OldestKept      string `json:"oldest_kept"`
	AuditDeleted    int64  `json:"audit_deleted"`
	AuditOldestKept string `json:"audit_oldest_kept"`
}

func decodeRetention(t *testing.T, rec *httptest.ResponseRecorder) retentionReport {
	t.Helper()
	var body retentionReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())
	return body
}

func TestRetentionRefusesWithoutTheToken(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RetentionToken = testRetentionToken

	require.Equal(t, http.StatusNotFound, retention(t, api.NewRouter(f.deps), "").Code)
}

func TestRetentionRefusesAWrongToken(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RetentionToken = testRetentionToken

	require.Equal(t, http.StatusNotFound, retention(t, api.NewRouter(f.deps), "wrong").Code)
}

// An unset token disables the endpoint rather than opening it. This is the
// test that matters most of the three: without it, one forgotten environment
// variable leaves a delete endpoint answering to whoever guesses the path.
func TestRetentionIsDisabledWhenNoTokenIsConfigured(t *testing.T) {
	f := newFixture(t)
	f.deps.Config.RetentionToken = ""

	require.Equal(t, http.StatusNotFound, retention(t, api.NewRouter(f.deps), "").Code)
	require.Equal(t, http.StatusNotFound, retention(t, api.NewRouter(f.deps), "anything").Code)
}

// errInjected is the failure interceptedDB hands back for a statement a test
// wants to fail.
var errInjected = errors.New("injected failure")

// interceptedDB answers the sqlc statements named in results itself and passes
// every other call through. A nil error answers "DELETE 0" without running the
// statement; any other error fails it. The match is on sqlc's own name line,
// which every generated query constant starts with.
type interceptedDB struct {
	db.DBTX
	results map[string]error
}

func (i interceptedDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	for statement, err := range i.results {
		if strings.Contains(sql, "-- name: "+statement+" ") {
			if err != nil {
				return pgconn.CommandTag{}, err
			}
			return pgconn.NewCommandTag("DELETE 0"), nil
		}
	}
	return i.DBTX.Exec(ctx, sql, args...)
}

// isolate runs the handler's queries in a transaction of the test's own,
// rolled back when the test ends, and returns it for seeding and reading back.
//
// Both deletes are instance-wide, and `go test ./...` runs internal/db's tests
// in a parallel process against the same database, with click-delete tests of
// their own that seed rows as old as 2026-01-01 and assert exact counts. Rows
// seeded here inside the transaction are invisible to that process, and
// nothing here commits, so the other package can neither delete nor count
// them, and nothing a test seeds outlives it even if the run is killed. The
// other direction is settled per test: a test whose click delete is real pins
// its clock so its cutoff lies below every row internal/db seeds, and one that
// does not need the click delete intercepts it.
func isolate(t *testing.T, f *fixture, results map[string]error) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(context.Background())
	require.NoError(t, err)
	// Registered after newFixture's own cleanup, so it runs first: the team
	// delete must not wait on rows this transaction still holds.
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	f.deps.Config.RetentionToken = testRetentionToken
	f.deps.Queries = db.New(interceptedDB{DBTX: tx, results: results})
	return tx
}

// skipClickDelete is for tests about the audit half: the click delete reports
// zero rows without running, so it cannot reach rows internal/db is counting.
var skipClickDelete = map[string]error{"DeleteExpiredClickStats": nil}

// aYearEarlier is the clock for the two tests whose click delete is real. Its
// cutoff, 2025-06-05, lies below every row internal/db's click-delete tests
// seed, which the fixture's own clock (cutoff 2026-06-05) does not.
func aYearEarlier() time.Time { return time.Date(2025, 9, 2, 12, 0, 0, 0, time.UTC) }

func seedClickRow(t *testing.T, tx pgx.Tx, linkID uuid.UUID, day string) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`insert into link_click_stats
		   (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
		 values ($1, $2::date, 'total', null, 1, 1)`, linkID, day)
	require.NoError(t, err)
}

// The clock is pinned to 2025-09-02, so the cutoff is 2025-06-05 — eighty-nine
// days earlier. The row on the cutoff day survives; the one before it does not.
func TestRetentionDeletesOnlyWhatIsPastTheCutoff(t *testing.T) {
	f := newFixture(t)
	f.deps.Now = aYearEarlier
	tx := isolate(t, f, nil)

	for _, d := range []string{"2025-06-04", "2025-06-05", "2025-09-01"} {
		seedClickRow(t, tx, f.linkID, d)
	}

	rec := retention(t, api.NewRouter(f.deps), testRetentionToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	report := decodeRetention(t, rec)
	require.EqualValues(t, 1, report.Deleted)
	require.Equal(t, "2025-06-05", report.OldestKept)

	var surviving int
	require.NoError(t, tx.QueryRow(context.Background(),
		`select count(*) from link_click_stats where link_id = $1`, f.linkID).Scan(&surviving))
	require.Equal(t, 2, surviving)
}

// Running twice is safe and the second run reports nothing. The workflow has
// no way to know whether an earlier attempt got through, so an endpoint that
// misbehaved on a repeat call would turn a retried run into a hazard.
func TestRetentionIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.deps.Now = aYearEarlier
	tx := isolate(t, f, nil)
	seedClickRow(t, tx, f.linkID, "2025-01-01")

	router := api.NewRouter(f.deps)

	first := decodeRetention(t, retention(t, router, testRetentionToken))
	second := decodeRetention(t, retention(t, router, testRetentionToken))

	require.EqualValues(t, 1, first.Deleted)
	require.EqualValues(t, 0, second.Deleted)
}

// insertAuditEntry writes one audit_log row at an exact instant, bypassing
// internal/audit because no real write path can backdate created_at. It goes
// through the test's transaction, so the rollback removes it; through the
// pool it would outlive its team, because audit_log.team_id is "on delete set
// null".
func insertAuditEntry(t *testing.T, tx pgx.Tx, teamID uuid.UUID, createdAt string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, tx.QueryRow(context.Background(),
		`insert into audit_log (team_id, action, entity_type, entity_id, metadata, created_at)
		 values ($1, 'team.renamed', 'team', $1, '{}'::jsonb, $2::timestamptz)
		 returning id`, teamID, createdAt).Scan(&id))
	return id
}

func fixtureTeamID(t *testing.T, f *fixture) uuid.UUID {
	t.Helper()
	var teamID uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select team_id from link where id = $1`, f.linkID).Scan(&teamID))
	return teamID
}

// The fixture pins the clock to 2026-09-02 12:00 UTC, so the audit floor is
// 2024-09-02 00:00 UTC — two calendar years back from the start of that day.
// An entry at exactly the floor survives; one second earlier does not. The
// rows are split across two teams because the delete is instance-wide on
// purpose, and a team filter creeping into it would show here as the second
// team's old row surviving.
func TestRetentionDeletesAuditEntriesOlderThanTwoYears(t *testing.T) {
	f := newFixture(t)
	tx := isolate(t, f, skipClickDelete)
	ctx := context.Background()

	teamA := fixtureTeamID(t, f)
	var teamB uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name, slug)
		 values ('fixture-b', 'fixture-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 12))
		 returning id`).Scan(&teamB))

	expiredA := insertAuditEntry(t, tx, teamA, "2024-09-01T23:59:59Z")
	expiredB := insertAuditEntry(t, tx, teamB, "2023-01-15T08:00:00Z")
	atTheFloor := insertAuditEntry(t, tx, teamA, "2024-09-02T00:00:00Z")
	recent := insertAuditEntry(t, tx, teamB, "2026-09-01T10:00:00Z")

	// The delete is instance-wide, so the count it reports includes any
	// committed row older than the floor — none, unless an earlier run of
	// auditlog_test.go was killed between seeding a backdated row and its
	// cleanup. Counting first keeps that leftover from failing this test
	// while still pinning the figure exactly.
	var expired int64
	require.NoError(t, tx.QueryRow(ctx,
		`select count(*) from audit_log where created_at < '2024-09-02T00:00:00Z'`).Scan(&expired))
	require.GreaterOrEqual(t, expired, int64(2))

	rec := retention(t, api.NewRouter(f.deps), testRetentionToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	report := decodeRetention(t, rec)
	require.Equal(t, expired, report.AuditDeleted)
	require.Equal(t, "2024-09-02", report.AuditOldestKept)
	require.Equal(t, "2026-06-05", report.OldestKept,
		"the click rollup's floor is unchanged by the audit delete running beside it")

	var surviving []int64
	rows, err := tx.Query(ctx,
		`select id from audit_log where id = any($1) order by id`,
		[]int64{expiredA, expiredB, atTheFloor, recent})
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		surviving = append(surviving, id)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.Equal(t, []int64{atTheFloor, recent}, surviving)
}

// The workflow's logs are read by key, so the response keeps the two keys it
// had before the audit log joined and only adds beside them. Decoding into a
// map is what notices a key being renamed: the struct decode above would
// silently leave a renamed field at its zero value.
func TestRetentionReportsBothTablesUnderStableKeys(t *testing.T) {
	f := newFixture(t)
	isolate(t, f, skipClickDelete)

	rec := retention(t, api.NewRouter(f.deps), testRetentionToken)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	require.ElementsMatch(t,
		[]string{"deleted", "oldest_kept", "audit_deleted", "audit_oldest_kept"}, keys)
}

// captureLogs points the fixture's logger at a buffer and returns it.
func captureLogs(f *fixture) *bytes.Buffer {
	var logs bytes.Buffer
	f.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	return &logs
}

// A failed click delete stops the run before the audit log is touched: the
// old audit entry is still there afterwards, and nothing claims otherwise.
func TestRetentionSkipsTheAuditDeleteWhenTheClickDeleteFails(t *testing.T) {
	f := newFixture(t)
	tx := isolate(t, f, map[string]error{"DeleteExpiredClickStats": errInjected})
	logs := captureLogs(f)
	expired := insertAuditEntry(t, tx, fixtureTeamID(t, f), "2023-01-15T08:00:00Z")

	rec := retention(t, api.NewRouter(f.deps), testRetentionToken)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, logs.String(), `msg="analytics retention failed"`)
	require.NotContains(t, logs.String(), "audit log retention",
		"the audit delete must not run after the click delete failed")

	var surviving int
	require.NoError(t, tx.QueryRow(context.Background(),
		`select count(*) from audit_log where id = $1`, expired).Scan(&surviving))
	require.Equal(t, 1, surviving)
}

// A failed audit delete is a failed run, even though the click delete before
// it succeeded. The response is the 500 alone rather than a report with one
// half filled in — the workflow withholds its heartbeat on it — and the log
// names the audit log, with its floor, under a message of its own.
func TestRetentionFailsWholeWhenTheAuditDeleteFails(t *testing.T) {
	f := newFixture(t)
	isolate(t, f, map[string]error{
		"DeleteExpiredClickStats": nil,
		"DeleteExpiredAuditLog":   errInjected,
	})
	logs := captureLogs(f)

	rec := retention(t, api.NewRouter(f.deps), testRetentionToken)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "deleted",
		"no partial report: the body must not carry the click delete's figures")
	require.Contains(t, logs.String(), `msg="analytics retention ran"`)
	require.Contains(t, logs.String(), `msg="audit log retention failed"`)
	require.Contains(t, logs.String(), "oldest_kept=2024-09-02")
}
