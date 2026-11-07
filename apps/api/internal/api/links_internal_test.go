package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/destination"
)

// TestResolveTagRefsEnforcesTheCapWithoutTouchingTheDatabase exists because
// Huma's own maxItems:"10" schema tag on CreateLinkInput.Body.TagIDs and
// UpdateLinkInput.Body.TagIDs already refuses a tag_ids array longer than
// maxTagsPerLink before either handler runs — the two limits are numerically
// identical today (both 10), which makes resolveTagRefs's own
// len(tagIDs) > maxTagsPerLink check unreachable through any HTTP request an
// api_test isolation test could send: TestCreateLinkEnforcesTheTagsPerLinkCap
// still passes with that check disabled, because Huma's schema validation
// rejects the oversized array first. The guard still matters as protection
// against a future drift between the schema tag and this constant — someone
// lowers maxTagsPerLink without remembering the two maxItems tags are a
// separate literal — so it is asserted here directly, bypassing Huma's
// binding entirely. Passing a nil *db.Queries proves the point: the guard
// must fire before touching the database at all.
func TestResolveTagRefsEnforcesTheCapWithoutTouchingTheDatabase(t *testing.T) {
	var d Deps
	tooMany := make([]uuid.UUID, maxTagsPerLink+1)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}

	_, err := d.resolveTagRefs(context.Background(), nil, uuid.New(), tooMany)

	var status huma.StatusError
	if !errors.As(err, &status) || status.GetStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("got %v, want a 422 without ever touching the database", err)
	}
}

// TestDestinationRejectedCarriesTheReasonAtTheField pins the 422 both link
// writes answer a refused destination with, without the database the handler
// tests need: the English detail stays, and one ErrorDetail at
// body.destination_url carries destination.Reason's token, which apps/web and
// apps/cli translate. A reworded message must not be able to break that, so
// the token is asserted on its own field.
func TestDestinationRejectedCarriesTheReasonAtTheField(t *testing.T) {
	for raw, token := range map[string]string{
		"http://example.org": "scheme",
		"https://10.0.0.1/":  "private_address",
	} {
		refusal := destination.Validate(raw, nil)
		require.Error(t, refusal, "%q", raw)

		var model *huma.ErrorModel
		require.ErrorAs(t, destinationRejected(refusal), &model)
		require.Equal(t, http.StatusUnprocessableEntity, model.Status)
		require.Equal(t, refusal.Error(), model.Detail)
		require.Equal(t, []*huma.ErrorDetail{{
			Location: "body.destination_url", Message: refusal.Error(), Value: token,
		}}, model.Errors)
	}
}

// An error destination.Reason cannot name gets the 422 it always had, with no
// detail: a client then falls back to its generic wording instead of reading
// an invented token.
func TestDestinationRejectedSendsNoDetailWithoutAReason(t *testing.T) {
	refusal := errors.New("destination: something new")

	var model *huma.ErrorModel
	require.ErrorAs(t, destinationRejected(refusal), &model)
	require.Equal(t, http.StatusUnprocessableEntity, model.Status)
	require.Equal(t, refusal.Error(), model.Detail)
	require.Empty(t, model.Errors)
}

// requireDestinationReason asserts the 422 a refused destination answers with
// carries token at body.destination_url.
func requireDestinationReason(t *testing.T, err error, token string) {
	t.Helper()
	var model *huma.ErrorModel
	require.ErrorAs(t, err, &model)
	require.Equal(t, http.StatusUnprocessableEntity, model.Status)
	require.Len(t, model.Errors, 1)
	require.Equal(t, "body.destination_url", model.Errors[0].Location)
	require.Equal(t, token, model.Errors[0].Value)
}

// TestCreateLinkSendsTheDestinationReasonWithoutTheDatabase drives the handler
// itself: the destination is validated before createLink touches the database
// or the cache, so the zero Deps proves both the wiring and that order. The
// database-backed TestCreateLinkSendsTheReasonForARefusedDestination checks
// the same through the router, where a local Postgres is running.
func TestCreateLinkSendsTheDestinationReasonWithoutTheDatabase(t *testing.T) {
	var d Deps
	in := &CreateLinkInput{}
	in.Body.DestinationURL = "http://example.org"

	_, err := d.createLink(context.Background(), in)

	requireDestinationReason(t, err, "scheme")
}

// TestUpdateLinkSendsTheDestinationReasonWithoutTheDatabase is the PATCH half
// of the test above, for the same reason.
func TestUpdateLinkSendsTheDestinationReasonWithoutTheDatabase(t *testing.T) {
	var d Deps
	in := &UpdateLinkInput{}
	raw := "https://10.0.0.1/"
	in.Body.DestinationURL = &raw

	_, err := d.updateLink(context.Background(), in)

	requireDestinationReason(t, err, "private_address")
}
