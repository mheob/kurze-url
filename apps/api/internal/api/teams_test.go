package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func TestCreateTeamIsRefusedForANonMaintainer(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost, "/v1/teams",
		map[string]string{"name": "Neuer Verein"})

	require.Equal(t, http.StatusForbidden, rec.Code,
		"team creation is maintainer-only; an admin of another team is still a stranger here")
	require.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
}

func TestCreateTeamMakesTheMaintainerTheOwnerAndAuditsIt(t *testing.T) {
	f := newTenancyFixture(t)
	maintainer := f.members[authz.RoleOwner]

	rec := f.do(t, maintainer, http.MethodPost, "/v1/teams",
		map[string]string{"name": "Neuer Verein"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	created := decode[api.Team](t, rec)
	require.Equal(t, "Neuer Verein", created.Name)
	require.Equal(t, "owner", created.Role)

	t.Cleanup(func() {
		_, _ = f.pool.Exec(t.Context(), `delete from team where id = $1`, created.ID)
	})

	var role string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select role from team_member where team_id = $1 and user_id = $2`,
		created.ID, maintainer.id).Scan(&role))
	require.Equal(t, "owner", role)

	var action string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action from audit_log where team_id = $1`, created.ID).Scan(&action))
	require.Equal(t, "team.created", action)
}

func TestCreateTeamRejectsAnEmptyName(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleOwner], http.MethodPost, "/v1/teams",
		map[string]string{"name": ""})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestListTeamsReturnsOnlyTheCallersTeams(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.Team]](t, rec)
	require.Len(t, page.Items, 1)
	require.Equal(t, f.teamID, page.Items[0].ID)
	require.Equal(t, "viewer", page.Items[0].Role)
	require.Equal(t, 1, page.TotalCount)
	require.Equal(t, 1, page.Page)
	require.Equal(t, 25, page.PerPage)
}

func TestListTeamsIsEmptyForAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet, "/v1/teams", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.Team]](t, rec)
	require.Empty(t, page.Items)
	require.Zero(t, page.TotalCount)
}

func TestListTeamsRejectsAnOversizedPerPage(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams?per_page=1000", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"per_page is capped at 100 by the schema, so an oversized value is a validation error")
}

func TestGetTeamIsVisibleToAViewer(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams/"+f.teamID.String(), nil)

	require.Equal(t, http.StatusOK, rec.Code)
	team := decode[api.Team](t, rec)
	require.Equal(t, f.teamID, team.ID)
	require.WithinDuration(t, time.Now(), team.CreatedAt, time.Hour)
}

func TestGetTeamHidesItFromAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet, "/v1/teams/"+f.teamID.String(), nil)

	require.Equal(t, http.StatusNotFound, rec.Code,
		"a non-member must not be able to distinguish an existing team from a missing one")
}

func TestGetTeamRejectsAnUnauthenticatedCaller(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, testUser{}, http.MethodGet, "/v1/teams/"+f.teamID.String(), nil)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRenameTeamRequiresAdmin(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Umbenannt"})

	require.Equal(t, http.StatusForbidden, rec.Code)

	var name string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select name from team where id = $1`, f.teamID).Scan(&name))
	require.NotEqual(t, "Umbenannt", name, "a refused request must change nothing")
}

func TestRenameTeamAuditsTheOldAndNewName(t *testing.T) {
	f := newTenancyFixture(t)

	var before string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select name from team where id = $1`, f.teamID).Scan(&before))

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Umbenannt"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "Umbenannt", decode[api.Team](t, rec).Name)

	var action string
	var metadata []byte
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action, metadata from audit_log where team_id = $1 and action = 'team.renamed'`,
		f.teamID).Scan(&action, &metadata))
	require.Contains(t, string(metadata), before)
	require.Contains(t, string(metadata), "Umbenannt")
}

func TestRenameTeamIs404ForAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Umbenannt"})

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetTeamRejectsAMalformedTeamID(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams/not-a-uuid", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.NotEqual(t, uuid.Nil.String(), rec.Body.String())
}
