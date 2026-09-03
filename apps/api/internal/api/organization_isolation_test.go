package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// tagPage mirrors folderPage in folders_test.go and linkPage in links_test.go:
// each list-returning test file defines its own page-shaped struct, one per
// item type, so a test can decode a response without reaching into the api
// package for its exported Page[T].
type tagPage struct {
	Items      []tagBody `json:"items"`
	Page       int       `json:"page"`
	PerPage    int       `json:"per_page"`
	TotalCount int       `json:"total_count"`
}

// This file mirrors links_isolation_test.go for folders and tags: every case
// asserts persisted database state, not only the response status, because a
// handler that answers 404 and writes anyway would pass a status-only test.

// TestAnotherTeamsFoldersAreInvisible is TestListLinksNeverShowsAnotherTeamsLinks's
// folder equivalent. Both fixtures seed a folder of their own named "fixture",
// so this checks that the foreign folder never appears — by id and by team —
// rather than asserting an empty list.
func TestAnotherTeamsFoldersAreInvisible(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	foreign := theirs.createFolder(t, "Fremd")

	rec := mine.do(t, mine.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+mine.teamID.String()+"/folders", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	page := decode[folderPage](t, rec)
	for _, item := range page.Items {
		require.Equal(t, mine.teamID, item.TeamID, "another team's folder leaked into the list")
		require.NotEqual(t, foreign.ID, item.ID, "another team's folder must not be listed")
	}
}

func TestAnotherTeamsFolderCannotBeRenamed(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	foreign := theirs.createFolder(t, "Fremd")

	rec := mine.do(t, mine.members[authz.RoleEditor], http.MethodPatch,
		"/v1/folders/"+foreign.ID.String(), map[string]any{"name": "Gekapert"})

	// Checked before the status code, and deliberately not gated on it: the
	// status code alone is not proof that nothing happened, since a scope bug
	// could still leave a 500 (from an unrelated failure downstream of the
	// mutation) while the write itself went through. Read the row back
	// through theirs' own database connection, not through the API, so this
	// does not depend on the same authorization path it is testing.
	var name string
	require.NoError(t, theirs.pool.QueryRow(context.Background(),
		`select name from folder where id = $1`, foreign.ID).Scan(&name))
	require.Equal(t, "Fremd", name, "the row must not have been modified despite the 404")

	require.Equal(t, http.StatusNotFound, rec.Code,
		"an editor of another team must not learn that this folder exists")
}

func TestAnotherTeamsFolderCannotBeDeleted(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	foreign := theirs.createFolder(t, "Fremd")

	rec := mine.do(t, mine.members[authz.RoleEditor], http.MethodDelete,
		"/v1/folders/"+foreign.ID.String(), nil)

	var exists bool
	require.NoError(t, theirs.pool.QueryRow(context.Background(),
		`select exists(select 1 from folder where id = $1)`, foreign.ID).Scan(&exists))
	require.True(t, exists, "the row must not have been deleted despite the 404")

	require.Equal(t, http.StatusNotFound, rec.Code,
		"an editor of another team must not learn that this folder exists")
}

// TestAnotherTeamsTagsAreInvisible is TestAnotherTeamsFoldersAreInvisible's tag
// equivalent.
func TestAnotherTeamsTagsAreInvisible(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	foreign := theirs.createTag(t, "Fremd")

	rec := mine.do(t, mine.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+mine.teamID.String()+"/tags", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	page := decode[tagPage](t, rec)
	for _, item := range page.Items {
		require.Equal(t, mine.teamID, item.TeamID, "another team's tag leaked into the list")
		require.NotEqual(t, foreign.ID, item.ID, "another team's tag must not be listed")
	}
}

func TestAnotherTeamsTagCannotBeRenamed(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	foreign := theirs.createTag(t, "Fremd")

	rec := mine.do(t, mine.members[authz.RoleEditor], http.MethodPatch,
		"/v1/tags/"+foreign.ID.String(), map[string]any{"name": "Gekapert"})

	var name string
	require.NoError(t, theirs.pool.QueryRow(context.Background(),
		`select name from tag where id = $1`, foreign.ID).Scan(&name))
	require.Equal(t, "Fremd", name, "the row must not have been modified despite the 404")

	require.Equal(t, http.StatusNotFound, rec.Code,
		"an editor of another team must not learn that this tag exists")
}

func TestAnotherTeamsTagCannotBeDeleted(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	foreign := theirs.createTag(t, "Fremd")

	rec := mine.do(t, mine.members[authz.RoleEditor], http.MethodDelete,
		"/v1/tags/"+foreign.ID.String(), nil)

	var exists bool
	require.NoError(t, theirs.pool.QueryRow(context.Background(),
		`select exists(select 1 from tag where id = $1)`, foreign.ID).Scan(&exists))
	require.True(t, exists, "the row must not have been deleted despite the 404")

	require.Equal(t, http.StatusNotFound, rec.Code,
		"an editor of another team must not learn that this tag exists")
}

// TestFilteringByAnotherTeamsTagReturnsNothing: the tag_id filter is a plain
// value, not an addressed resource, so a foreign tag_id is an empty page
// rather than a 404 — and it must not become a 500 either.
func TestFilteringByAnotherTeamsTagReturnsNothing(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	foreign := theirs.createTag(t, "Fremd")
	mine.createLink(t, "meins", "https://example.org/mine")

	rec := mine.do(t, mine.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+mine.teamID.String()+"/links?tag_id="+foreign.ID.String(), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[linkPage](t, rec)
	require.Empty(t, page.Items, "no link of mine carries another team's tag")
}
