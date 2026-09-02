package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

func TestGetLinkForRedirectResolvesVerifiedDomainOnly(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	q := db.New(pool)

	row, err := q.GetLinkForRedirect(ctx, db.GetLinkForRedirectParams{
		Hostname: "short.test",
		Slug:     "hello",
	})

	require.NoError(t, err, "the seeded link on the verified domain must resolve")
	require.Equal(t, "https://example.org/hello", row.DestinationURL)
	require.EqualValues(t, 302, row.RedirectType)
	require.Equal(t, "active", row.State)
	require.False(t, row.HasPassword)
	require.True(t, row.AnalyticsEnabled)
	require.Nil(t, row.ExpiresAt)
}

func TestGetLinkForRedirectMissesUnknownSlug(t *testing.T) {
	ctx := context.Background()
	q := db.New(testPool(t))

	_, err := q.GetLinkForRedirect(ctx, db.GetLinkForRedirectParams{
		Hostname: "short.test",
		Slug:     "does-not-exist",
	})

	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestUpsertClickStatsAccumulatesAcrossBatches(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	q := db.New(pool)

	linkID := uuid.MustParse("00000000-0000-0000-0000-0000000000d1")
	day := time.Now().UTC().Truncate(24 * time.Hour)
	firefox := "Firefox"

	params := []db.UpsertClickStatsParams{
		{LinkID: linkID, BucketStart: day, DimensionType: "total", DimensionValue: nil, Clicks: 2, UniqueVisitors: 1},
		{LinkID: linkID, BucketStart: day, DimensionType: "browser", DimensionValue: &firefox, Clicks: 2, UniqueVisitors: 1},
	}

	require.NoError(t, execBatch(ctx, q, params))
	require.NoError(t, execBatch(ctx, q, params))

	var totalClicks, totalUnique int64
	require.NoError(t, pool.QueryRow(ctx,
		`select clicks, unique_visitors from link_click_stats
		 where link_id = $1 and bucket_start = $2 and dimension_type = 'total'`,
		linkID, day).Scan(&totalClicks, &totalUnique))

	require.EqualValues(t, 4, totalClicks)
	require.EqualValues(t, 2, totalUnique)

	// Leave the table as we found it so reruns stay deterministic.
	_, err := pool.Exec(ctx, `delete from link_click_stats where link_id = $1`, linkID)
	require.NoError(t, err)
}

func execBatch(ctx context.Context, q *db.Queries, params []db.UpsertClickStatsParams) error {
	var execErr error
	results := q.UpsertClickStats(ctx, params)
	results.Exec(func(_ int, err error) {
		if err != nil && execErr == nil {
			execErr = err
		}
	})
	if err := results.Close(); err != nil && execErr == nil {
		execErr = err
	}
	return execErr
}
