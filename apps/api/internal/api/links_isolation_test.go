package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// isolationAttempt is one operation a stranger team's owner tries against a
// link that is not theirs. A slice, not a map: map iteration order is
// randomised, which would otherwise reorder these subtests on every run and
// make a failure harder to reproduce.
type isolationAttempt struct {
	name   string
	method string
	body   any
}

// TestALinkIsInvisibleToEveryOtherTeam is the whole point of the entity scope.
// Two independent fixtures means two independent teams, and the owner of one is
// a total stranger to the other.
func TestALinkIsInvisibleToEveryOtherTeam(t *testing.T) {
	mine := newTenancyFixture(t)
	theirs := newTenancyFixture(t)
	victim := theirs.createLink(t, "vertraulich", "https://example.org/vertraulich")
	path := "/v1/links/" + victim.ID.String()

	for _, tc := range []isolationAttempt{
		{"read", http.MethodGet, nil},
		{"update", http.MethodPatch, map[string]any{"state": "disabled"}},
		{"delete", http.MethodDelete, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := mine.do(t, mine.members[authz.RoleOwner], tc.method, path, tc.body)

			// Checked before the status code, and deliberately not gated on
			// it succeeding or failing: the status code alone is not proof
			// that nothing happened, since a scope bug could still leave a
			// 500 (say, from an unrelated failure downstream of the
			// mutation) while the write itself went through. Read the row
			// back through theirs' own database connection — not through the
			// API, so this assertion does not depend on the same
			// authorization path it is testing — and confirm it still
			// matches what createLink produced. Ordered first so a `require`
			// failure on the status-code check below can never skip it.
			if tc.name == "update" || tc.name == "delete" {
				assertLinkRowUnchanged(t, theirs.pool, victim.ID, victim.State, victim.DestinationURL)
			}

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

// assertLinkRowUnchanged reads a link row directly from Postgres and fails
// the test if it is missing or no longer matches the given state and
// destination. It bypasses the API entirely: an update or delete leaking
// across teams must be caught here even if the handler that attempted it
// answered with a status code that looks like a refusal.
func assertLinkRowUnchanged(
	t *testing.T, pool *pgxpool.Pool, linkID uuid.UUID, wantState, wantDestination string,
) {
	t.Helper()

	var state, destination string
	err := pool.QueryRow(context.Background(),
		`select state, destination_url from link where id = $1`, linkID).Scan(&state, &destination)
	require.NoError(t, err, "the link must still exist in the database, unmodified")
	require.Equal(t, wantState, state, "the link's state must not have changed")
	require.Equal(t, wantDestination, destination, "the link's destination must not have changed")
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
