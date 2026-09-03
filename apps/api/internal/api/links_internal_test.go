package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
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
