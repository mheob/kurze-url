package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

	linkID := createTestLink(ctx, t, pool)
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
}

// createTestLink inserts a dedicated team, domain and link so the test owns
// its rollup rows instead of borrowing the seeded `hello` link, which the API
// server's own redirect handler (and any manual smoke test) also writes to.
// Deleting the team on cleanup cascades to the domain, link and its
// link_click_stats rows.
func createTestLink(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	var userID, teamID, domainID, linkID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('click-stats-test') returning id`).Scan(&teamID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		teamID, "t"+uuid.NewString()[:8]+".test").Scan(&domainID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, created_by)
		 values ($1, $2, 'click-stats', 'https://example.org/click-stats', $3) returning id`,
		domainID, teamID, userID).Scan(&linkID))

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})

	return linkID
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
