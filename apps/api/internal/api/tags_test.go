package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// tagCapForTests mirrors maxTagsPerTeam in internal/api/limits.go. This file
// runs in api_test, not api, so it cannot see that unexported constant
// directly; a drift between the two would only ever loosen this test (the cap
// enforcement itself is exercised against whatever the real constant says).
const tagCapForTests = 200

type tagBody struct {
	ID     uuid.UUID `json:"id"`
	TeamID uuid.UUID `json:"team_id"`
	Name   string    `json:"name"`
}

// createTag mirrors createFolder in folders_test.go.
func (f *tenancyFixture) createTag(t *testing.T, name string) tagBody {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/tags",
		map[string]any{"name": name})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	return decode[tagBody](t, rec)
}

// createLinkWithTags seeds a link already tagged with tagIDs, by inserting
// directly rather than going through the create-link endpoint: that endpoint
// does not accept tag_ids yet (a later plan wires tags into link
// create/update), so the only way to get a link tagged today is a raw insert,
// mirroring how createLinkInFolder seeds a filed link.
func (f *tenancyFixture) createLinkWithTags(t *testing.T, dest string, tagIDs ...uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`insert into link (domain_id, team_id, slug, destination_url, created_by)
		 values ($1, $2, $3, $4, $5) returning id`,
		f.teamDomainID, f.teamID, "taglink-"+uuid.NewString()[:8], dest,
		f.members[authz.RoleOwner].id).Scan(&id))

	if len(tagIDs) > 0 {
		_, err := f.pool.Exec(context.Background(),
			`insert into link_tag (link_id, tag_id) select $1, unnest($2::uuid[])`,
			id, tagIDs)
		require.NoError(t, err)
	}

	return id
}

func TestCreateTagKeepsTheTypedCase(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/tags",
		map[string]any{"name": "Sommerfest"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[tagBody](t, rec)
	require.Equal(t, "Sommerfest", body.Name, "the capital must be kept")
	require.Equal(t, f.teamID, body.TeamID, "tag was created for the wrong team")
}

func TestCreateTagRejectsACaseInsensitiveDuplicate(t *testing.T) {
	f := newTenancyFixture(t)

	first := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/tags",
		map[string]any{"name": "Sommerfest"})
	require.Equal(t, http.StatusCreated, first.Code, "body: %s", first.Body.String())

	second := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/tags",
		map[string]any{"name": "SOMMERFEST"})
	require.Equal(t, http.StatusConflict, second.Code, "body: %s", second.Body.String())

	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from tag where team_id = $1 and name ilike 'sommerfest'`,
		f.teamID).Scan(&count))
	require.Equal(t, 1, count, "the duplicate must not have been persisted")
}

func TestTwoTeamsMayEachHaveTheSameTagName(t *testing.T) {
	// Uniqueness is per team. Without the team_id half of the index this would
	// be a 409 and one Verein could block a name for every other. Each
	// fixture is its own team, so creating the same name against two
	// independent fixtures exercises exactly that.
	for range 2 {
		f := newTenancyFixture(t)
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
			"/v1/teams/"+f.teamID.String()+"/tags",
			map[string]any{"name": "Presse"})
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	}
}

func TestUpdateTagRejectsACaseInsensitiveCollision(t *testing.T) {
	f := newTenancyFixture(t)
	f.createTag(t, "Presse")
	other := f.createTag(t, "Sommerfest")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch,
		"/v1/tags/"+other.ID.String(), map[string]any{"name": "presse"})

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
}

func TestCreateTagEnforcesTheTeamCap(t *testing.T) {
	f := newTenancyFixture(t)

	// Seed to one below the cap directly: going through the endpoint 200 times
	// would make this test the slowest in the suite for no extra coverage.
	// The fixture itself seeds one tag of its own ("fixture"), so seeding one
	// fewer here still lands exactly on the cap — the same adjustment
	// TestCreateFolderEnforcesTheTeamCap makes for the fixture's own folder.
	_, err := f.pool.Exec(context.Background(),
		`insert into tag (team_id, name)
		 select $1, 'seed-' || g from generate_series(1, $2) g`,
		f.teamID, tagCapForTests-1)
	require.NoError(t, err)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/tags",
		map[string]any{"name": "one too many"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"body: %s", rec.Body.String())

	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from tag where team_id = $1`, f.teamID).Scan(&count))
	require.Equal(t, tagCapForTests, count, "the cap must have held")
}

func TestDeleteTagDetachesItWithoutTouchingTheLinks(t *testing.T) {
	f := newTenancyFixture(t)
	tag := f.createTag(t, "Presse")
	linkID := f.createLinkWithTags(t, "https://example.org/pm", tag.ID)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/tags/"+tag.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())

	var joins int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from link_tag where link_id = $1`, linkID).Scan(&joins))
	require.Equal(t, 0, joins, "the join row must be gone")

	var exists bool
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select exists(select 1 from link where id = $1)`, linkID).Scan(&exists))
	require.True(t, exists, "deleting a tag must not delete its links")
}
