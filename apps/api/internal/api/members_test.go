package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func TestListMembersIsVisibleToAViewerAndCarriesEmails(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/members", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[api.Page[api.Member]](t, rec)
	require.Equal(t, 4, page.TotalCount, "the fixture seeds one member per role")

	byEmail := map[string]string{}
	for _, member := range page.Items {
		byEmail[member.Email] = member.Role
	}
	require.Equal(t, "owner", byEmail[f.members[authz.RoleOwner].email])
	require.Equal(t, "viewer", byEmail[f.members[authz.RoleViewer].email])
}

func TestListMembersPaginates(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/members?page=2&per_page=2", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.Member]](t, rec)
	require.Len(t, page.Items, 2)
	require.Equal(t, 2, page.Page)
	require.Equal(t, 2, page.PerPage)
	require.Equal(t, 4, page.TotalCount)
}

func TestListMembersHidesTheTeamFromAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/members", nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}
