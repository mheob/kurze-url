package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
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
		`insert into team (name, slug)
		 values ($1, 'verein-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 12))
		 returning id`, "Anderer Verein").Scan(&otherTeamID))
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

// seedAuditEntry writes one audit_log row for the fixture's team at an exact
// instant, which no real write path can do. It is removed by id on cleanup:
// audit_log.team_id is "on delete set null", so the team's own cleanup would
// leave a backdated row behind for the retention tests to count.
func seedAuditEntry(t *testing.T, f *tenancyFixture, createdAt string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`insert into audit_log (team_id, action, entity_type, entity_id, metadata, created_at)
		 values ($1, 'team.renamed', 'team', $1, '{}'::jsonb, $2::timestamptz)
		 returning id`, f.teamID, createdAt).Scan(&id))
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `delete from audit_log where id = $1`, id)
	})
	return id
}

func auditEntryIDs(page api.AuditLogPage) []int64 {
	ids := make([]int64, 0, len(page.Items))
	for _, entry := range page.Items {
		ids = append(ids, entry.ID)
	}
	return ids
}

// With the clock at 2026-09-02 the floor is 2024-09-02 00:00 UTC. An entry
// older than that may still exist — the daily job has not run yet — but the
// endpoint must not show what the job is about to delete, whether the reader
// asked for an earlier from or for none at all.
func TestAuditLogNeverServesAnEntryOlderThanTheFloor(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-02")

	older := seedAuditEntry(t, f, "2024-09-01T23:59:59Z")
	atTheFloor := seedAuditEntry(t, f, "2024-09-02T00:00:00Z")

	for _, query := range []string{"?per_page=100", "?per_page=100&from=2020-01-01T00:00:00Z"} {
		rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet, auditLogPath(f)+query, nil)

		require.Equal(t, http.StatusOK, rec.Code, "query %s, body: %s", query, rec.Body.String())
		page := decode[api.AuditLogPage](t, rec)
		require.Equal(t, []int64{atTheFloor}, auditEntryIDs(page), "query %s", query)
		require.NotContains(t, auditEntryIDs(page), older)
		require.Equal(t, 1, page.TotalCount, "query %s", query)
	}
}

// retained_since is the floor's day, read off the same function the
// retention job deletes by — the web filter's min comes from here.
func TestAuditLogReportsTheDayItIsRetainedSince(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-02")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet, auditLogPath(f), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "2024-09-02", decode[api.AuditLogPage](t, rec).RetainedSince)
}

// The clamp only ever raises from. A from inside the retained window is the
// reader's own choice and passes through untouched.
func TestAuditLogLeavesAFromAfterTheFloorAlone(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-02")

	seedAuditEntry(t, f, "2025-01-01T12:00:00Z")
	later := seedAuditEntry(t, f, "2025-06-01T12:00:00Z")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?from=2025-03-01T00:00:00Z", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[api.AuditLogPage](t, rec)
	require.Equal(t, []int64{later}, auditEntryIDs(page))
	require.Equal(t, 1, page.TotalCount)
}

// A to before the floor asks for a window whose entries are all gone. That is
// an empty page, not a 422: the request is well formed, and a bookmarked
// filter becomes exactly this once the floor has moved past it.
func TestAuditLogAnswersAnEmptyPageForAToBeforeTheFloor(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-02")

	seedAuditEntry(t, f, "2024-08-01T12:00:00Z")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?to=2024-08-31T23:59:59Z", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[api.AuditLogPage](t, rec)
	require.Empty(t, page.Items)
	require.Equal(t, 0, page.TotalCount)
	require.Equal(t, "2024-09-02", page.RetainedSince)
}

// A page past the end recovers its total from a separate count. That count
// has to run with the same clamped from as the page, or it would report an
// entry the endpoint refuses to show: three rows exist, two are served.
func TestAuditLogFallbackCountUsesTheClampedFrom(t *testing.T) {
	f := newTenancyFixture(t)
	pinToday(t, f, "2026-09-02")

	seedAuditEntry(t, f, "2024-09-01T12:00:00Z")
	seedAuditEntry(t, f, "2025-01-01T12:00:00Z")
	seedAuditEntry(t, f, "2026-01-01T12:00:00Z")

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodGet,
		auditLogPath(f)+"?from=2020-01-01T00:00:00Z&page=99&per_page=1", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[api.AuditLogPage](t, rec)
	require.Empty(t, page.Items)
	require.Equal(t, 2, page.TotalCount)
}

// TestAuditLogPageSchemaIsTheFlatEnvelope reads the generated document rather
// than the Go, because the Go cannot show the two ways this goes wrong. Huma
// could nest the embedded Page under its own property instead of splicing its
// fields in, which would move items one level down for every client. And an
// optional or nullable retained_since would generate `retained_since?: string
// | null`, which the web filter would then have to guard against a value the
// handler always sends.
func TestAuditLogPageSchemaIsTheFlatEnvelope(t *testing.T) {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)

	schema := humaAPI.OpenAPI().Components.Schemas.Map()["AuditLogPage"]
	require.NotNil(t, schema)

	for _, name := range []string{"items", "page", "per_page", "total_count", "retained_since"} {
		require.Contains(t, schema.Properties, name)
		require.Contains(t, schema.Required, name)
	}
	retainedSince := schema.Properties["retained_since"]
	require.Equal(t, "string", retainedSince.Type)
	require.False(t, retainedSince.Nullable)
}
