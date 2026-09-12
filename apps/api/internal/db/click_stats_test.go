package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// seedClick writes one rollup row directly. The recorder's own upsert path has
// its own tests; what matters here is what the read queries make of rows that
// already exist. Cleanup is inherited: link_click_stats.link_id cascades from
// link, which cascades from the team newLinkFixture removes.
func seedClick(
	t *testing.T, pool *pgxpool.Pool, linkID uuid.UUID,
	day, dimensionType string, dimensionValue *string, clicks, unique int64,
) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`insert into link_click_stats
		   (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
		 values ($1, $2::date, $3, $4, $5, $6)`,
		linkID, day, dimensionType, dimensionValue, clicks, unique)
	require.NoError(t, err)
}

func strptr(v string) *string { return &v }

// day parses a YYYY-MM-DD literal into the UTC midnight the date column maps
// to, so a test can pass the same value to seedClick and to the query.
func day(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	require.NoError(t, err)
	return parsed
}

// TestGetLinkClickSeriesSumsPerDayAndSplitsBots is the query's whole reason for
// existing: one scan answers both the all-clicks figures (the 'total' rows) and
// the human ones (the bot_status = 'human' rows). Every click writes both, so
// the two are exactly comparable — which no second query could guarantee.
func TestGetLinkClickSeriesSumsPerDayAndSplitsBots(t *testing.T) {
	f := newLinkFixture(t)
	pool := testPool(t)
	link := f.create(t, "series")

	seedClick(t, pool, link.ID, "2026-09-01", "total", nil, 10, 6)
	seedClick(t, pool, link.ID, "2026-09-01", "bot_status", strptr("human"), 7, 4)
	seedClick(t, pool, link.ID, "2026-09-01", "bot_status", strptr("bot"), 3, 2)
	// A day nobody human reached: the filtered sum is null, and coalesce must
	// make that a zero rather than a missing column.
	seedClick(t, pool, link.ID, "2026-09-03", "total", nil, 2, 2)
	seedClick(t, pool, link.ID, "2026-09-03", "bot_status", strptr("bot"), 2, 2)

	rows, err := f.queries.GetLinkClickSeries(context.Background(), db.GetLinkClickSeriesParams{
		LinkID:  link.ID,
		FromDay: day(t, "2026-09-01"),
		ToDay:   day(t, "2026-09-03"),
	})
	require.NoError(t, err)

	// 2026-09-02 is absent, not zero: the table has no row for a day nobody
	// clicked. Filling that gap is Go's job, in Task 3.
	require.Len(t, rows, 2)
	require.Equal(t, day(t, "2026-09-01"), rows[0].BucketStart.UTC())
	require.EqualValues(t, 10, rows[0].Clicks)
	require.EqualValues(t, 6, rows[0].UniqueVisitors)
	require.EqualValues(t, 7, rows[0].HumanClicks)
	require.EqualValues(t, 4, rows[0].HumanUniqueVisitors)
	require.Equal(t, day(t, "2026-09-03"), rows[1].BucketStart.UTC())
	require.EqualValues(t, 2, rows[1].Clicks)
	require.EqualValues(t, 0, rows[1].HumanClicks)
	require.EqualValues(t, 0, rows[1].HumanUniqueVisitors)
}

// TestGetLinkClickSeriesExcludesOtherDaysAndOtherLinks pins both filters at
// once. The link filter is the tenancy-relevant one: this query runs behind a
// scope that has already resolved the team, but it must still not aggregate
// another link's rows into this link's answer.
func TestGetLinkClickSeriesExcludesOtherDaysAndOtherLinks(t *testing.T) {
	f := newLinkFixture(t)
	pool := testPool(t)
	link := f.create(t, "window")

	seedClick(t, pool, link.ID, "2026-08-31", "total", nil, 99, 99)
	seedClick(t, pool, link.ID, "2026-09-02", "total", nil, 4, 4)
	seedClick(t, pool, link.ID, "2026-09-05", "total", nil, 99, 99)
	seedClick(t, pool, f.otherLinkID, "2026-09-02", "total", nil, 99, 99)

	rows, err := f.queries.GetLinkClickSeries(context.Background(), db.GetLinkClickSeriesParams{
		LinkID:  link.ID,
		FromDay: day(t, "2026-09-01"),
		ToDay:   day(t, "2026-09-03"),
	})
	require.NoError(t, err)

	require.Len(t, rows, 1)
	require.EqualValues(t, 4, rows[0].Clicks)
}

// TestGetLinkClickBreakdownsCapsValuesAndReportsTheRemainder is what makes the
// one-document response safe: referrer is an unbounded dimension, so without
// the cap an outsider could steer the response's size by sending distinct
// referrers. The remainder columns are how a caller learns what the cap hid.
func TestGetLinkClickBreakdownsCapsValuesAndReportsTheRemainder(t *testing.T) {
	f := newLinkFixture(t)
	pool := testPool(t)
	link := f.create(t, "breakdown")

	// Eleven referrers with 11, 10, … 1 clicks. Sum is 66; the top ten hold 65
	// of that, so the eleventh is exactly the remainder.
	for i := 1; i <= 11; i++ {
		seedClick(t, pool, link.ID, "2026-09-02", "referrer",
			strptr(fmt.Sprintf("ref%02d.example", i)), int64(i), int64(i))
	}

	rows, err := f.queries.GetLinkClickBreakdowns(context.Background(),
		db.GetLinkClickBreakdownsParams{
			LinkID:    link.ID,
			FromDay:   day(t, "2026-09-01"),
			ToDay:     day(t, "2026-09-03"),
			TopValues: 10,
		})
	require.NoError(t, err)

	require.Len(t, rows, 10)
	require.Equal(t, "referrer", rows[0].DimensionType)
	require.Equal(t, "ref11.example", *rows[0].DimensionValue)
	require.EqualValues(t, 11, rows[0].Clicks)
	require.EqualValues(t, 2, rows[9].Clicks, "the tenth-largest value is 2, not 1")
	// Every row carries the dimension's full figures, so Go can subtract the
	// rows it received from them to get the remainder.
	require.EqualValues(t, 66, rows[0].DimensionClicks)
	require.EqualValues(t, 66, rows[0].DimensionUniqueVisitors)
	require.EqualValues(t, 11, rows[0].DimensionValues)
}

// TestGetLinkClickBreakdownsOrdersTiesByValue pins the tie-break. Without it
// two values with equal clicks could swap places between requests, and which
// one the cap drops would become nondeterministic.
func TestGetLinkClickBreakdownsOrdersTiesByValue(t *testing.T) {
	f := newLinkFixture(t)
	pool := testPool(t)
	link := f.create(t, "ties")

	seedClick(t, pool, link.ID, "2026-09-02", "country", strptr("DE"), 5, 5)
	seedClick(t, pool, link.ID, "2026-09-02", "country", strptr("AT"), 5, 5)

	rows, err := f.queries.GetLinkClickBreakdowns(context.Background(),
		db.GetLinkClickBreakdownsParams{
			LinkID:    link.ID,
			FromDay:   day(t, "2026-09-01"),
			ToDay:     day(t, "2026-09-03"),
			TopValues: 10,
		})
	require.NoError(t, err)

	require.Len(t, rows, 2)
	require.Equal(t, "AT", *rows[0].DimensionValue)
	require.Equal(t, "DE", *rows[1].DimensionValue)
}

// TestGetLinkClickBreakdownsExcludesTheTotalDimension keeps the two queries
// from double-counting: 'total' is the series' source, and including it here
// would add a phantom breakdown whose value is null.
func TestGetLinkClickBreakdownsExcludesTheTotalDimension(t *testing.T) {
	f := newLinkFixture(t)
	pool := testPool(t)
	link := f.create(t, "nototal")

	seedClick(t, pool, link.ID, "2026-09-02", "total", nil, 9, 9)
	seedClick(t, pool, link.ID, "2026-09-02", "device", strptr("mobile"), 9, 9)

	rows, err := f.queries.GetLinkClickBreakdowns(context.Background(),
		db.GetLinkClickBreakdownsParams{
			LinkID:    link.ID,
			FromDay:   day(t, "2026-09-01"),
			ToDay:     day(t, "2026-09-03"),
			TopValues: 10,
		})
	require.NoError(t, err)

	require.Len(t, rows, 1)
	require.Equal(t, "device", rows[0].DimensionType)
}

// TestDeleteExpiredClickStatsKeepsTheOldestServedDay is the boundary the whole
// job turns on. The stats endpoint serves bucket_start >= today-89, so the row
// at exactly the cutoff is the oldest one a caller can still see. Deleting it
// would make a Verein's statistics vanish from inside a window the API still
// offers — and unlike deleting too little, that cannot be undone.
func TestDeleteExpiredClickStatsKeepsTheOldestServedDay(t *testing.T) {
	f := newLinkFixture(t)
	pool := testPool(t)
	link := f.create(t, "retention")

	for _, d := range []string{"2026-06-03", "2026-06-04", "2026-06-05", "2026-09-01"} {
		seedClick(t, pool, link.ID, d, "total", nil, 1, 1)
	}

	deleted, err := f.queries.DeleteExpiredClickStats(context.Background(), day(t, "2026-06-05"))
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted, "only the two days strictly before the cutoff")

	rows, err := pool.Query(context.Background(),
		`select bucket_start from link_click_stats where link_id = $1 order by bucket_start`,
		link.ID)
	require.NoError(t, err)
	defer rows.Close()

	var remaining []time.Time
	for rows.Next() {
		var bucket time.Time
		require.NoError(t, rows.Scan(&bucket))
		remaining = append(remaining, bucket.UTC())
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []time.Time{day(t, "2026-06-05"), day(t, "2026-09-01")}, remaining,
		"the cutoff day itself survives")
}

// TestDeleteExpiredClickStatsReportsZeroWithoutErroring pins what this job
// does every day for its first eighty days in production: nothing is old
// enough to delete yet. That has to be a zero rather than an error, and the
// count has to come back, because "0 rows" in a workflow log is the only
// evidence that separates a working job from one that stopped running.
func TestDeleteExpiredClickStatsReportsZeroWithoutErroring(t *testing.T) {
	f := newLinkFixture(t)
	pool := testPool(t)
	link := f.create(t, "nothing-old")
	seedClick(t, pool, link.ID, "2026-09-01", "total", nil, 5, 5)

	deleted, err := f.queries.DeleteExpiredClickStats(context.Background(), day(t, "2026-06-05"))

	require.NoError(t, err)
	require.EqualValues(t, 0, deleted)
}

// TestDeleteExpiredClickStatsLeavesTheAuditLogAlone pins the scope decision.
// No document promises audit_log a retention period, and it is the record of
// who changed what — the thing one consults precisely when something went
// wrong months ago. Deleting it has to be its own decision, never a side
// effect of this job growing a "while we're at it" clause.
func TestDeleteExpiredClickStatsLeavesTheAuditLogAlone(t *testing.T) {
	f := newLinkFixture(t)
	pool := testPool(t)
	link := f.create(t, "audit-untouched")
	seedClick(t, pool, link.ID, "2026-01-01", "total", nil, 1, 1)

	var auditID int64
	require.NoError(t, pool.QueryRow(context.Background(),
		`insert into audit_log (team_id, actor_user_id, action, entity_type, entity_id, metadata, created_at)
		 values ($1, $2, 'link.created', 'link', $3, '{}'::jsonb, '2026-01-01T00:00:00Z')
		 returning id`,
		f.teamID, f.userID, link.ID).Scan(&auditID))

	deleted, err := f.queries.DeleteExpiredClickStats(context.Background(), day(t, "2026-06-05"))
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)

	var surviving int
	require.NoError(t, pool.QueryRow(context.Background(),
		`select count(*) from audit_log where id = $1`, auditID).Scan(&surviving))
	require.Equal(t, 1, surviving, "audit_log is out of this job's scope, on purpose")
}
