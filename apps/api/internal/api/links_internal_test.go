package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
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

// longDestination is an ASCII https URL exactly one byte past
// destination.MaxLength.
func longDestination() string {
	const prefix = "https://example.org/"
	return prefix + strings.Repeat("a", destination.MaxLength+1-len(prefix))
}

// linkWriteBodySchema is the request body schema of one link write, from the
// same registration cmd/openapi documents, so it is the schema Huma checks a
// request against. A zero Deps registers without touching anything.
func linkWriteBodySchema(t *testing.T, path, method string) (huma.Registry, *huma.Schema) {
	t.Helper()
	humaAPI := humago.New(http.NewServeMux(), NewHumaConfig())
	Deps{}.RegisterV1(humaAPI)

	doc := humaAPI.OpenAPI()
	item := doc.Paths[path]
	require.NotNil(t, item, path)
	operation := item.Post
	if method == http.MethodPatch {
		operation = item.Patch
	}
	require.NotNil(t, operation, "%s %s", method, path)

	registry := doc.Components.Schemas
	schema := registry.SchemaFromRef(operation.RequestBody.Content["application/json"].Schema.Ref)
	require.NotNil(t, schema, "%s %s", method, path)
	return registry, schema
}

// TestLinkWritesLeaveTheDestinationsLengthToTheHandler pins why neither link
// write bounds destination_url in its schema. With a maxLength there, Huma
// refuses an over-long destination itself, before the handler runs, with a
// detail valued with the URL rather than a token, and too_long is unreachable
// over HTTP: the reasoning SetLinkPasswordInput gives for its password. The
// limit is stated in the field's doc instead, and this keeps that doc and
// destination.MaxLength on the same number.
func TestLinkWritesLeaveTheDestinationsLengthToTheHandler(t *testing.T) {
	for _, tc := range []struct{ name, path, method string }{
		{"create", "/v1/teams/{team_id}/links", http.MethodPost},
		{"update", "/v1/links/{link_id}", http.MethodPatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, schema := linkWriteBodySchema(t, tc.path, tc.method)

			res := &huma.ValidateResult{}
			huma.Validate(registry, schema, huma.NewPathBuffer([]byte("body"), len("body")),
				huma.ModeWriteToServer, map[string]any{"destination_url": longDestination()}, res)
			// Location and message only: the value is the 2049-byte URL.
			var refusals []string
			for _, err := range res.Errors {
				var detail *huma.ErrorDetail
				if errors.As(err, &detail) {
					refusals = append(refusals, detail.Location+": "+detail.Message)
				}
			}
			require.Empty(t, refusals, "the length is destination.Validate's to refuse")
			require.Len(t, res.Errors, len(refusals))

			field := schema.Properties["destination_url"]
			require.NotNil(t, field)
			require.Contains(t, field.Description, strconv.Itoa(destination.MaxLength)+" bytes",
				"the doc must state the limit destination.Validate enforces")
		})
	}
}

// TestCreateLinkRefusesALongDestinationAsTooLongWithoutTheDatabase is the
// handler's half of the test above: what Huma now lets through, createLink
// refuses with the too_long token like every other refusal.
func TestCreateLinkRefusesALongDestinationAsTooLongWithoutTheDatabase(t *testing.T) {
	var d Deps
	in := &CreateLinkInput{}
	in.Body.DestinationURL = longDestination()

	_, err := d.createLink(context.Background(), in)

	requireDestinationReason(t, err, "too_long")
}

// TestUpdateLinkRefusesALongDestinationAsTooLongWithoutTheDatabase is the
// PATCH half of the test above.
func TestUpdateLinkRefusesALongDestinationAsTooLongWithoutTheDatabase(t *testing.T) {
	var d Deps
	in := &UpdateLinkInput{}
	raw := longDestination()
	in.Body.DestinationURL = &raw

	_, err := d.updateLink(context.Background(), in)

	requireDestinationReason(t, err, "too_long")
}
