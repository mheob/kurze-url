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
//
// pathSuffix is appended to the base link path — empty for the four
// operations that live directly on it, "/password" for the two that live on
// the password subresource instead.
type isolationAttempt struct {
	name       string
	method     string
	pathSuffix string
	body       any
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
		{"read", http.MethodGet, "", nil},
		{"update", http.MethodPatch, "", map[string]any{"state": "disabled"}},
		{"delete", http.MethodDelete, "", nil},
		{"set password", http.MethodPut, "/password", map[string]any{"password": "Kartoffelsalat!7"}},
		{"remove password", http.MethodDelete, "/password", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := mine.do(t, mine.members[authz.RoleOwner], tc.method, path+tc.pathSuffix, tc.body)

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
			if tc.name != "read" {
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
	body := decode[linkBody](t, rec)
	require.Equal(t, "active", body.State)
	require.False(t, body.HasPassword, "the password attempts above must not have protected the link")
}

// assertLinkRowUnchanged reads a link row directly from Postgres and fails
// the test if it is missing or no longer matches the given state and
// destination. It bypasses the API entirely: an update or delete leaking
// across teams must be caught here even if the handler that attempted it
// answered with a status code that looks like a refusal.
//
// password_hash is checked the same way for the same reason: victim links in
// this suite are always created without a password, so it must still read
// NULL afterwards — the one column a cross-team "set password" attempt could
// have written without moving state or destination_url at all.
func assertLinkRowUnchanged(
	t *testing.T, pool *pgxpool.Pool, linkID uuid.UUID, wantState, wantDestination string,
) {
	t.Helper()

	var state, destination string
	var passwordHash *string
	err := pool.QueryRow(context.Background(),
		`select state, destination_url, password_hash from link where id = $1`, linkID).
		Scan(&state, &destination, &passwordHash)
	require.NoError(t, err, "the link must still exist in the database, unmodified")
	require.Equal(t, wantState, state, "the link's state must not have changed")
	require.Equal(t, wantDestination, destination, "the link's destination must not have changed")
	require.Nil(t, passwordHash, "the link's password_hash must still be unset")
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
