package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
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

// count(*) over () is only readable off a row the paginated query actually
// returns, so a page past the end has nothing to read it from without a
// fallback. This asserts the fallback recovers the true total rather than
// reporting 0, as it would before the fix.
func TestAuditLogOutOfRangePageReportsTheTrueTotal(t *testing.T) {
	f := newTenancyFixture(t)

	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Erst"}).Code)
	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Zweit"}).Code)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?page=99&per_page=1", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	page := decode[api.Page[api.AuditEntry]](t, rec)
	require.Empty(t, page.Items, "page 99 is well past the last page of 2 entries at 1 per page")
	require.Equal(t, 2, page.TotalCount,
		"the true total must still be reported even though this page is empty")
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

// TestAuditLogNeverCrossesTeams closes the one gap the structural argument
// ("team_id = $1::uuid is applied unconditionally, unlike the optional
// filters") does not itself cover with a test: nothing previously proved
// that a *second* team's rows are absent from the first team's feed over
// this HTTP endpoint. The fixture only ever wires one team, so the second
// team and its audit entry are seeded directly with the pool — the point
// here is the assertion, not the setup route.
func TestAuditLogNeverCrossesTeams(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()

	// An entry for team one, through the real write path.
	require.Equal(t, http.StatusOK, f.do(t, f.members[authz.RoleAdmin], http.MethodPatch,
		"/v1/teams/"+f.teamID.String(), map[string]string{"name": "Team Eins"}).Code)

	// A second, unrelated team with its own audit entry.
	var otherTeamID uuid.UUID
	require.NoError(t, f.pool.QueryRow(ctx,
		`insert into team (name) values ($1) returning id`, "Anderer Verein").Scan(&otherTeamID))
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `delete from team where id = $1`, otherTeamID)
	})

	const marker = "ONLY-IN-THE-OTHER-TEAM"
	_, err := f.pool.Exec(ctx,
		`insert into audit_log (team_id, actor_user_id, action, entity_type, entity_id, metadata)
		 values ($1, $2, 'team.renamed', 'team', $1, $3)`,
		otherTeamID, f.members[authz.RoleAdmin].id, []byte(`{"to":"`+marker+`"}`))
	require.NoError(t, err)

	// per_page=100 is well above the two entries now in play, so a dropped
	// team_id predicate would surface the other team's row here rather than
	// hide behind pagination.
	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?per_page=100", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.NotContains(t, rec.Body.String(), marker,
		"the other team's audit entry must never appear in this team's feed")
	require.NotContains(t, rec.Body.String(), otherTeamID.String(),
		"the other team's id must never appear in this team's feed")

	page := decode[api.Page[api.AuditEntry]](t, rec)
	require.Equal(t, 1, page.TotalCount)
	require.Len(t, page.Items, 1)
	require.Equal(t, f.teamID, *page.Items[0].EntityID,
		"the only entry returned must be the one belonging to team one, identified by its entity_id")
}
