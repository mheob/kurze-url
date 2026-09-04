package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// folderCapForTests mirrors maxFoldersPerTeam in internal/api/limits.go. This
// file runs in api_test, not api, so it cannot see that unexported constant
// directly; a drift between the two would only ever loosen this test (the cap
// enforcement itself is exercised against whatever the real constant says).
const folderCapForTests = 100

type folderBody struct {
	ID        uuid.UUID `json:"id"`
	TeamID    uuid.UUID `json:"team_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type folderPage struct {
	Items      []folderBody `json:"items"`
	Page       int          `json:"page"`
	PerPage    int          `json:"per_page"`
	TotalCount int          `json:"total_count"`
}

// createFolder mirrors createLink in links_test.go.
func (f *tenancyFixture) createFolder(t *testing.T, name string) folderBody {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/folders",
		map[string]any{"name": name})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	return decode[folderBody](t, rec)
}

// createLinkInFolder seeds a link already filed into folderID, by inserting
// directly rather than going through the create-link endpoint: that endpoint
// does not accept folder_id yet (a later plan wires folders into link
// create/update), so the only way to get a link into a folder today is the
// way the fixture seeds its own initial link — a raw insert.
func (f *tenancyFixture) createLinkInFolder(t *testing.T, dest string, folderID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`insert into link (domain_id, team_id, slug, destination_url, folder_id, created_by)
		 values ($1, $2, $3, $4, $5, $6) returning id`,
		f.teamDomainID, f.teamID, "folderlink-"+uuid.NewString()[:8], dest, folderID,
		f.members[authz.RoleOwner].id).Scan(&id))
	return id
}

func TestCreateFolderStoresTrimmedName(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/folders",
		map[string]any{"name": "  Sommerfest 2026  "})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[folderBody](t, rec)
	require.Equal(t, "Sommerfest 2026", body.Name)
	require.Equal(t, f.teamID, body.TeamID, "folder was created for the wrong team")
}

func TestCreateFolderLeavesParentFolderIDNull(t *testing.T) {
	// Folders are flat in this iteration. parent_folder_id stays in the schema
	// for a later plan, and the API must never write it — a property that
	// decays silently, so it is asserted rather than assumed.
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/folders",
		map[string]any{"name": "Presse"})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var nulls, total int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from folder where team_id = $1 and parent_folder_id is null`,
		f.teamID).Scan(&nulls))
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from folder where team_id = $1`, f.teamID).Scan(&total))

	require.Equal(t, total, nulls, "the API must never set a parent_folder_id")
	require.NotZero(t, total)
}

func TestCreateFolderRejectsEmptyName(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/folders",
		map[string]any{"name": "   "})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestCreateFolderEnforcesTheTeamCap(t *testing.T) {
	f := newTenancyFixture(t)

	// Seed to the cap directly: going through the endpoint 100 times would
	// make this test the slowest in the suite for no extra coverage. The
	// fixture itself seeds one folder of its own, so seeding one fewer here
	// still lands exactly on the cap — the same adjustment
	// TestListLinksReturnsThePageEnvelope makes for the fixture's own link.
	_, err := f.pool.Exec(context.Background(),
		`insert into folder (team_id, name)
		 select $1, 'seed-' || g from generate_series(1, $2) g`,
		f.teamID, folderCapForTests-1)
	require.NoError(t, err)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/folders",
		map[string]any{"name": "one too many"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"body: %s", rec.Body.String())

	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from folder where team_id = $1`, f.teamID).Scan(&count))
	require.Equal(t, folderCapForTests, count, "the cap must have held")
}

func TestListFoldersOrdersByName(t *testing.T) {
	f := newTenancyFixture(t)

	for _, name := range []string{"Zeltlager", "Ausflug", "Presse"} {
		f.createFolder(t, name)
	}

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/folders", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[folderPage](t, rec)

	// The fixture seeds a folder of its own named "fixture"; it is excluded
	// here because this test cares only about the relative order of the
	// folders it created, not where the fixture's own entry happens to sort.
	var names []string
	for _, item := range page.Items {
		if item.Name == "fixture" {
			continue
		}
		names = append(names, item.Name)
	}

	require.Equal(t, []string{"Ausflug", "Presse", "Zeltlager"}, names)
}

func TestUpdateFolderRenamesIt(t *testing.T) {
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Alter Name")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch,
		"/v1/folders/"+folder.ID.String(), map[string]any{"name": "  Neuer Name  "})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[folderBody](t, rec)
	require.Equal(t, "Neuer Name", body.Name)
	require.Equal(t, folder.ID, body.ID)

	var count int
	var metadata string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*), coalesce(max(metadata::text), '') from audit_log
		 where team_id = $1 and action = 'folder.updated' and entity_id = $2`,
		f.teamID, folder.ID).Scan(&count, &metadata))
	require.Equal(t, 1, count)
	require.Contains(t, metadata, "Neuer Name")
}

func TestDeleteFolderUnfilesItsLinks(t *testing.T) {
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest")
	linkID := f.createLinkInFolder(t, "https://example.org/fest", folder.ID)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/folders/"+folder.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())

	// The link survives, unfiled. Nothing is destroyed, which is why a
	// non-empty folder is not refused.
	var folderID *uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select folder_id from link where id = $1`, linkID).Scan(&folderID))
	require.Nil(t, folderID, "the link must still exist, unfiled")
}

func TestDeleteFolderRecordsHowManyLinksWereUnfiled(t *testing.T) {
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest")
	f.createLinkInFolder(t, "https://example.org/a", folder.ID)
	f.createLinkInFolder(t, "https://example.org/b", folder.ID)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/folders/"+folder.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())

	var unfiled int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select (metadata->>'links_unfiled')::int from audit_log
		 where team_id = $1 and action = 'folder.deleted'`, f.teamID).Scan(&unfiled))
	require.Equal(t, 2, unfiled)
}

func TestDeleteFolderRecordsWhichFolderWentInTheAuditRow(t *testing.T) {
	// entity_id points at a row that no longer exists, so without the name the
	// audit entry says only that some folder was deleted. DeleteTag records its
	// name for the same reason.
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest 2026")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/folders/"+folder.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())

	var name string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select metadata->>'name' from audit_log
		 where team_id = $1 and action = 'folder.deleted' and entity_id = $2`,
		f.teamID, folder.ID).Scan(&name))
	require.Equal(t, "Sommerfest 2026", name,
		"the audit row must name the folder, with its case intact")
}
