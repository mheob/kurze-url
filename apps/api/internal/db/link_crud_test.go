package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// linkFixture is one team with one verified domain, plus a second team with
// its own domain and link, so every tenancy assertion has something to leak to.
type linkFixture struct {
	queries  *db.Queries
	userID   uuid.UUID
	teamID   uuid.UUID
	domainID uuid.UUID
	hostname string

	otherTeamID uuid.UUID
	otherLinkID uuid.UUID
}

func newLinkFixture(t *testing.T) *linkFixture {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()

	f := &linkFixture{queries: db.New(pool)}
	require.NoError(t, pool.QueryRow(ctx, `select id from auth.users limit 1`).Scan(&f.userID))

	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('links') returning id`).Scan(&f.teamID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into team (name) values ('other links') returning id`).Scan(&f.otherTeamID))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from team where id = any($1)`,
			[]uuid.UUID{f.teamID, f.otherTeamID})
	})

	f.hostname = "l" + uuid.NewString()[:8] + ".test"
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		f.teamID, f.hostname).Scan(&f.domainID))

	var otherDomainID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`insert into domain (team_id, hostname, verification_status, verified_at)
		 values ($1, $2, 'verified', now()) returning id`,
		f.otherTeamID, "o"+uuid.NewString()[:8]+".test").Scan(&otherDomainID))
	require.NoError(t, pool.QueryRow(ctx,
		`insert into link (domain_id, team_id, slug, destination_url, created_by)
		 values ($1, $2, 'secret', 'https://example.org/secret', $3) returning id`,
		otherDomainID, f.otherTeamID, f.userID).Scan(&f.otherLinkID))

	return f
}

func (f *linkFixture) create(t *testing.T, slug string) db.CreateLinkRow {
	t.Helper()
	row, err := f.queries.CreateLink(context.Background(), db.CreateLinkParams{
		DomainID:         f.domainID,
		TeamID:           f.teamID,
		Slug:             slug,
		DestinationURL:   "https://example.org/" + slug,
		RedirectType:     302,
		AnalyticsEnabled: true,
		CreatedBy:        f.userID,
	})
	require.NoError(t, err)
	return row
}

func TestCreateLinkReturnsTheFullRepresentation(t *testing.T) {
	f := newLinkFixture(t)

	row := f.create(t, "sommerfest")

	require.Equal(t, "sommerfest", row.Slug)
	require.Equal(t, f.teamID, row.TeamID)
	require.Equal(t, f.hostname, row.Hostname)
	require.Equal(t, "active", row.State)
	require.False(t, row.HasPassword)
	require.Nil(t, row.ExpiresAt)
}

func TestCreateLinkRefusesADuplicateSlugOnOneDomain(t *testing.T) {
	f := newLinkFixture(t)
	f.create(t, "jhv")

	_, err := f.queries.CreateLink(context.Background(), db.CreateLinkParams{
		DomainID:         f.domainID,
		TeamID:           f.teamID,
		Slug:             "jhv",
		DestinationURL:   "https://example.org/other",
		RedirectType:     302,
		AnalyticsEnabled: true,
		CreatedBy:        f.userID,
	})
	require.Error(t, err, "(domain_id, slug) is unique")
}

func TestGetLinkForAPIFiltersByTeam(t *testing.T) {
	f := newLinkFixture(t)
	mine := f.create(t, "mine")
	ctx := context.Background()

	got, err := f.queries.GetLinkForAPI(ctx, db.GetLinkForAPIParams{ID: mine.ID, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, mine.ID, got.ID)

	_, err = f.queries.GetLinkForAPI(ctx,
		db.GetLinkForAPIParams{ID: f.otherLinkID, TeamID: f.teamID})
	require.Error(t, err, "another team's link must not be readable")
}

func TestGetLinkScopeResolvesWithoutATeamFilter(t *testing.T) {
	f := newLinkFixture(t)
	ctx := context.Background()

	got, err := f.queries.GetLinkScope(ctx, f.otherLinkID)
	require.NoError(t, err,
		"the scope lookup is what determines the team, so it cannot filter by one")
	require.Equal(t, f.otherTeamID, got.TeamID)
}

func TestListLinksForTeamNeverReturnsAnotherTeamsLinks(t *testing.T) {
	f := newLinkFixture(t)
	f.create(t, "one")
	f.create(t, "two")

	rows, err := f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID:  f.teamID,
		SortAsc: false,
		Limit:   50,
		Offset:  0,
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, f.teamID, row.TeamID)
		require.NotEqual(t, f.otherLinkID, row.ID)
	}
	require.Equal(t, int64(2), rows[0].TotalCount)
}

func TestListLinksForTeamFilters(t *testing.T) {
	f := newLinkFixture(t)
	f.create(t, "sommerfest")
	disabled := f.create(t, "winterfeier")
	_, err := f.queries.UpdateLink(context.Background(), db.UpdateLinkParams{
		ID:               disabled.ID,
		TeamID:           f.teamID,
		Slug:             disabled.Slug,
		DestinationURL:   disabled.DestinationURL,
		RedirectType:     disabled.RedirectType,
		State:            "disabled",
		AnalyticsEnabled: true,
	})
	require.NoError(t, err)

	state := "disabled"
	rows, err := f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, State: &state, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "winterfeier", rows[0].Slug)

	query := "sommer"
	rows, err = f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, Q: &query, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "sommerfest", rows[0].Slug)

	domainID := f.domainID
	rows, err = f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, DomainID: &domainID, Limit: 50,
	})
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

func TestListLinksForTeamSortsBothWays(t *testing.T) {
	f := newLinkFixture(t)
	first := f.create(t, "first")
	time.Sleep(10 * time.Millisecond)
	second := f.create(t, "second")

	desc, err := f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, SortAsc: false, Limit: 50,
	})
	require.NoError(t, err)
	require.Equal(t, second.ID, desc[0].ID, "newest first is the default")

	asc, err := f.queries.ListLinksForTeam(context.Background(), db.ListLinksForTeamParams{
		TeamID: f.teamID, SortAsc: true, Limit: 50,
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, asc[0].ID)
}

func TestUpdateLinkFiltersByTeamAndTouchesUpdatedAt(t *testing.T) {
	f := newLinkFixture(t)
	mine := f.create(t, "changeme")
	ctx := context.Background()

	updated, err := f.queries.UpdateLink(ctx, db.UpdateLinkParams{
		ID:               mine.ID,
		TeamID:           f.teamID,
		Slug:             "changed",
		DestinationURL:   "https://example.org/changed",
		RedirectType:     301,
		State:            "active",
		AnalyticsEnabled: false,
	})
	require.NoError(t, err)
	require.Equal(t, "changed", updated.Slug)
	require.Equal(t, int16(301), updated.RedirectType)
	require.False(t, updated.AnalyticsEnabled)
	require.True(t, updated.UpdatedAt.After(mine.UpdatedAt))

	_, err = f.queries.UpdateLink(ctx, db.UpdateLinkParams{
		ID:               f.otherLinkID,
		TeamID:           f.teamID,
		Slug:             "hijacked",
		DestinationURL:   "https://evil.test/",
		RedirectType:     302,
		State:            "active",
		AnalyticsEnabled: true,
	})
	require.Error(t, err, "another team's link must not be writable")
}

func TestDeleteLinkFiltersByTeam(t *testing.T) {
	f := newLinkFixture(t)
	mine := f.create(t, "goodbye")
	ctx := context.Background()

	affected, err := f.queries.DeleteLink(ctx, db.DeleteLinkParams{ID: mine.ID, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)

	affected, err = f.queries.DeleteLink(ctx,
		db.DeleteLinkParams{ID: f.otherLinkID, TeamID: f.teamID})
	require.NoError(t, err)
	require.Equal(t, int64(0), affected, "another team's link must survive")
}
