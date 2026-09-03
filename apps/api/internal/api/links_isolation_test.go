package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// TestALinkIsInvisibleToEveryOtherTeam is the whole point of the entity scope.
// Two independent fixtures means two independent teams, and the owner of one is
// a total stranger to the other.
func TestALinkIsInvisibleToEveryOtherTeam(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	victim := theirs.createLink(t, "vertraulich", "https://example.org/vertraulich")
	path := "/v1/links/" + victim.ID.String()

	for name, tc := range map[string]struct {
		method string
		body   any
	}{
		"read":   {http.MethodGet, nil},
		"update": {http.MethodPatch, map[string]any{"state": "disabled"}},
		"delete": {http.MethodDelete, nil},
	} {
		t.Run(name, func(t *testing.T) {
			rec := mine.do(t, mine.members[authz.RoleOwner], tc.method, path, tc.body)

			require.Equal(t, http.StatusNotFound, rec.Code,
				"an owner of another team must not learn that this link exists")
			require.NotContains(t, rec.Body.String(), "vertraulich")
			require.NotContains(t, rec.Body.String(), "403")
		})
	}

	// And nothing was actually done to it.
	rec := theirs.do(t, theirs.members[authz.RoleViewer], http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "active", decode[linkBody](t, rec).State)
}

func TestAStrangerSeesNoLinksAtAll(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "intern", "https://example.org/intern")

	require.Equal(t, http.StatusNotFound,
		f.do(t, f.stranger, http.MethodGet, "/v1/links/"+created.ID.String(), nil).Code)
	require.Equal(t, http.StatusNotFound,
		f.do(t, f.stranger, http.MethodGet, "/v1/teams/"+f.teamID.String()+"/links", nil).Code)
}

// TestCreatedLinkResolvesThroughTheRedirectPath closes the loop: the endpoints
// this plan adds and the hot path plan 1 built have to agree about the same
// link, including the case-insensitivity Task 6 introduced.
func TestCreatedLinkResolvesThroughTheRedirectPath(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "sommerfest", "https://example.org/sommerfest")

	rec := f.redirect(t, created.Hostname, "SommerFest")

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "https://example.org/sommerfest", rec.Header().Get("Location"))
}

// TestDeletedLinkStopsResolving is the other half of that loop.
func TestDeletedLinkStopsResolving(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "abgesagt", "https://example.org/abgesagt")
	require.Equal(t, http.StatusFound, f.redirect(t, created.Hostname, created.Slug).Code)

	require.Equal(t, http.StatusNoContent,
		f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
			"/v1/links/"+created.ID.String(), nil).Code)

	require.Equal(t, http.StatusNotFound,
		f.redirect(t, created.Hostname, created.Slug).Code)
}
