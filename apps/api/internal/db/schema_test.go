package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// testPool connects to the local Supabase Postgres. Tests skip with a clear
// message when it is not running, so `go test ./...` stays usable offline.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://postgres:postgres@127.0.0.1:54322/postgres"
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("local Supabase Postgres unavailable (%v) — run `supabase start`", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func TestSlugIsUniquePerDomainNotGlobally(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var teamID, userID string
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('t') returning id`).Scan(&teamID))
	require.NoError(t, tx.QueryRow(ctx,
		`select id from auth.users limit 1`).Scan(&userID))

	var domainA, domainB string
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname) values ($1, 'a.test') returning id`,
		teamID).Scan(&domainA))
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname) values ($1, 'b.test') returning id`,
		teamID).Scan(&domainB))

	insert := `insert into link (domain_id, team_id, slug, destination_url, created_by)
	           values ($1, $2, 'dup', 'https://example.org', $3)`

	_, err = tx.Exec(ctx, insert, domainA, teamID, userID)
	require.NoError(t, err, "same slug on the first domain")

	_, err = tx.Exec(ctx, insert, domainB, teamID, userID)
	require.NoError(t, err, "same slug on a different domain must be allowed")

	_, err = tx.Exec(ctx, insert, domainA, teamID, userID)
	require.Error(t, err, "same slug twice on the same domain must be rejected")
}

func TestTotalRollupRowIncrementsInsteadOfDuplicating(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var teamID, userID, domainID, linkID string
	require.NoError(t, tx.QueryRow(ctx,
		`insert into team (name) values ('t') returning id`).Scan(&teamID))
	require.NoError(t, tx.QueryRow(ctx,
		`select id from auth.users limit 1`).Scan(&userID))
	require.NoError(t, tx.QueryRow(ctx,
		`insert into domain (team_id, hostname) values ($1, 'c.test') returning id`,
		teamID).Scan(&domainID))
	require.NoError(t, tx.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, created_by)
		 values ($1, $2, 's', 'https://example.org', $3) returning id`,
		domainID, teamID, userID).Scan(&linkID))

	upsert := `insert into link_click_stats
	             (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
	           values ($1, current_date, 'total', null, 1, 1)
	           on conflict (link_id, bucket_start, dimension_type, dimension_value)
	           do update set clicks = link_click_stats.clicks + excluded.clicks,
	                         unique_visitors = link_click_stats.unique_visitors + excluded.unique_visitors`

	_, err = tx.Exec(ctx, upsert, linkID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, upsert, linkID)
	require.NoError(t, err)

	var rows, clicks int
	require.NoError(t, tx.QueryRow(ctx,
		`select count(*), coalesce(sum(clicks), 0) from link_click_stats
		 where link_id = $1 and dimension_type = 'total'`, linkID).Scan(&rows, &clicks))

	require.Equal(t, 1, rows, "the total row must not duplicate — needs UNIQUE NULLS NOT DISTINCT")
	require.Equal(t, 2, clicks)
}
