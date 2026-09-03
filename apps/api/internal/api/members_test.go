package api_test

import (
	"errors"
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

func TestAddMemberRequiresAdmin(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": "neu@verein.test", "role": "editor"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, f.invites.calls, "a refused request must not send an email")
}

func TestAddMemberAddsAnExistingAccountWithoutSendingEmail(t *testing.T) {
	f := newTenancyFixture(t)
	existing := f.stranger

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": existing.email, "role": "editor"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	added := decode[api.Member](t, rec)
	require.Equal(t, existing.id, added.UserID)
	require.Equal(t, "editor", added.Role)
	require.Empty(t, f.invites.calls,
		"an address that already has an account gets a membership, not an invitation")

	var action string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action from audit_log
		 where team_id = $1 and entity_id = $2`, f.teamID, existing.id).Scan(&action))
	require.Equal(t, "team_member.added", action)
}

func TestAddMemberInvitesAnUnknownAddress(t *testing.T) {
	f := newTenancyFixture(t)
	// The fake inviter itself seeds auth.users under the ID it reports, the
	// same way a real Supabase invite creates that row as a side effect —
	// team_member's foreign key needs the row to exist by the time the
	// handler inserts the membership.

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": "invited@verein.test", "role": "viewer"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, []string{"invited@verein.test"}, f.invites.calls)
	require.Equal(t, []map[string]any{{"team_id": f.teamID.String(), "role": "viewer"}}, f.invites.metadata,
		"the invitation must carry the team and the granted role so Supabase records it")

	var action string
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select action from audit_log where team_id = $1 and entity_id = $2`,
		f.teamID, f.invites.userID).Scan(&action))
	require.Equal(t, "team_member.invited", action)
}

func TestAddMemberRejectsSomeoneAlreadyInTheTeam(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.members[authz.RoleViewer].email, "role": "editor"})

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Empty(t, f.invites.calls)
}

func TestAddMemberRefusesAnAdminGrantingOwner(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.stranger.email, "role": "owner"})

	require.Equal(t, http.StatusForbidden, rec.Code,
		"granting the owner role requires owner")
}

func TestAddMemberLetsAnOwnerGrantOwner(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleOwner], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.stranger.email, "role": "owner"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
}

func TestAddMemberRejectsAnUnknownRole(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.stranger.email, "role": "superuser"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestAddMemberIsRateLimited(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.InviteRateLimitPerHour = 1
	f.rebuildRouter()

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": f.stranger.email, "role": "viewer"})
	require.Equal(t, http.StatusCreated, first.Code, "body: %s", first.Body.String())

	second := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": "another@verein.test", "role": "viewer"})

	require.Equal(t, http.StatusTooManyRequests, second.Code,
		"invitations spend real email quota, so the endpoint is capped per team")
}

func TestAddMemberSurfacesAnInviteFailureAs502(t *testing.T) {
	f := newTenancyFixture(t)
	f.invites.err = errors.New("supabase is unreachable")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/members",
		map[string]string{"email": "unknown@verein.test", "role": "viewer"})

	require.Equal(t, http.StatusBadGateway, rec.Code)

	var count int
	require.NoError(t, f.pool.QueryRow(t.Context(),
		`select count(*) from audit_log where team_id = $1`, f.teamID).Scan(&count))
	require.Zero(t, count, "a failed invitation must leave no membership and no audit entry")
}
