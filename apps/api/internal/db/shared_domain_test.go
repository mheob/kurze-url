package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

func TestUpsertSharedDomainIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	hostname := "shared-" + uuid.NewString()[:8] + ".test"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from domain where hostname = $1`, hostname)
	})

	first, err := queries.UpsertSharedDomain(ctx, hostname)
	require.NoError(t, err)

	second, err := queries.UpsertSharedDomain(ctx, hostname)
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID, "a second boot must not create a second row")

	var teamID *uuid.UUID
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`select team_id, verification_status from domain where id = $1`, first.ID).
		Scan(&teamID, &status))
	require.Nil(t, teamID, "a shared domain belongs to no team")
	require.Equal(t, "verified", status, "an unverified domain serves no links")
}

func TestUpsertSharedDomainRefusesToHijackATeamsDomain(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	hostname := "owned-" + uuid.NewString()[:8] + ".test"
	var teamID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('owner of a custom domain') returning id`).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})
	_, err := pool.Exec(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now())`, teamID, hostname)
	require.NoError(t, err)

	_, err = queries.UpsertSharedDomain(ctx, hostname)
	require.Error(t, err, "a team's own hostname must never be converted into a shared one")

	var stillOwned uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`select team_id from domain where hostname = $1`, hostname).Scan(&stillOwned))
	require.Equal(t, teamID, stillOwned)
}

func TestGetLinkableDomainAcceptsSharedAndOwnRejectsOthers(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	var mine, theirs uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `insert into team (name) values ('mine') returning id`).Scan(&mine))
	require.NoError(t, pool.QueryRow(ctx, `insert into team (name) values ('theirs') returning id`).Scan(&theirs))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = any($1)`,
			[]uuid.UUID{mine, theirs})
	})

	sharedHost := "shared-" + uuid.NewString()[:8] + ".test"
	shared, err := queries.UpsertSharedDomain(ctx, sharedHost)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from domain where id = $1`, shared.ID)
	})

	var ownVerified, ownPending, otherTeams uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		mine, "own-"+uuid.NewString()[:8]+".test").Scan(&ownVerified))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status)
		 values ($1, $2, 'pending') returning id`,
		mine, "pending-"+uuid.NewString()[:8]+".test").Scan(&ownPending))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		theirs, "other-"+uuid.NewString()[:8]+".test").Scan(&otherTeams))

	for name, tc := range map[string]struct {
		domainID uuid.UUID
		ok       bool
	}{
		"shared domain":            {shared.ID, true},
		"own verified domain":      {ownVerified, true},
		"own unverified domain":    {ownPending, false},
		"another team's domain":    {otherTeams, false},
		"a domain that is not one": {uuid.New(), false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := queries.GetLinkableDomain(ctx, db.GetLinkableDomainParams{
				ID: tc.domainID, TeamID: mine,
			})
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}
