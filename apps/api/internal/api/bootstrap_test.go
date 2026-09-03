package api_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

func TestProvisionSharedDomainIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	hostname := "boot-" + uuid.NewString()[:8] + ".test"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from domain where hostname = $1`, hostname)
	})

	first, err := api.ProvisionSharedDomain(ctx, queries, hostname)
	require.NoError(t, err)
	require.Equal(t, hostname, first.Hostname)
	require.NotEqual(t, uuid.Nil, first.ID)

	second, err := api.ProvisionSharedDomain(ctx, queries, hostname)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
}

func TestProvisionSharedDomainFailsOnATeamsHostname(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	queries := db.New(pool)

	hostname := "claimed-" + uuid.NewString()[:8] + ".test"
	var teamID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('claimant') returning id`).Scan(&teamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = $1`, teamID)
	})
	_, err := pool.Exec(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now())`, teamID, hostname)
	require.NoError(t, err)

	_, err = api.ProvisionSharedDomain(ctx, queries, hostname)
	require.ErrorIs(t, err, api.ErrHostnameClaimed)
}

func TestProvisionSharedDomainRejectsAnEmptyHostname(t *testing.T) {
	_, err := api.ProvisionSharedDomain(context.Background(), nil, "")
	require.Error(t, err)
}
