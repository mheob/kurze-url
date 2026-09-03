package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func auditLogPath(f *tenancyFixture) string {
	return "/v1/teams/" + f.teamID.String() + "/audit-log"
}

func TestAuditLogIsHiddenFromEditorsAndViewers(t *testing.T) {
	f := newTenancyFixture(t)

	for _, role := range []authz.Role{authz.RoleViewer, authz.RoleEditor} {
		rec := f.do(t, f.members[role], http.MethodGet, auditLogPath(f), nil)
		require.Equal(t, http.StatusForbidden, rec.Code, "role %s", role)
	}
}

func TestAuditLogListsEntriesForAnAdmin(t *testing.T) {
	f := newTenancyFixture(t)

	// Produce two entries through the API itself, so the test covers the real
	// write path rather than hand-inserted rows.
	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Erst"}).Code)
	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Zweit"}).Code)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet, auditLogPath(f), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[api.Page[api.AuditEntry]](t, rec)
	require.Equal(t, 2, page.TotalCount)
	require.Equal(t, "team.renamed", page.Items[0].Action)
	require.Equal(t, f.members[authz.RoleAdmin].id, *page.Items[0].ActorUserID)
	require.NotEmpty(t, page.Items[0].Metadata)
}

func TestAuditLogFiltersByAction(t *testing.T) {
	f := newTenancyFixture(t)

	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Erst"}).Code)
	require.Equal(t, http.StatusNoContent, f.do(t, f.members[authz.RoleAdmin], http.MethodDelete,
		memberPath(f, f.members[authz.RoleViewer]), nil).Code)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?action=team_member.removed", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.AuditEntry]](t, rec)
	require.Equal(t, 1, page.TotalCount)
	require.Equal(t, "team_member.removed", page.Items[0].Action)
}

func TestAuditLogRejectsAMalformedActorFilter(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?actor_user_id=not-a-uuid", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestAuditLogIs404ForAStranger(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.stranger, http.MethodGet, auditLogPath(f), nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}
