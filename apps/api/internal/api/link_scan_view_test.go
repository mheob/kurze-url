package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// scanView is the nested verdict as a client reads it.
type scanView struct {
	Verdict     string     `json:"verdict"`
	ThreatTypes []string   `json:"threat_types"`
	Since       *time.Time `json:"since"`
	CheckedAt   time.Time  `json:"checked_at"`
}

type linkWithScan struct {
	State string    `json:"state"`
	Scan  *scanView `json:"scan"`
}

// getLinkView reads one link as a viewer and returns it decoded and raw: the
// raw body is what tells an omitted key from a null one.
func getLinkView(t *testing.T, f *tenancyFixture, id uuid.UUID) (linkWithScan, string) {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+id.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	return decode[linkWithScan](t, rec), rec.Body.String()
}

func applyForTest(t *testing.T, f *tenancyFixture, id uuid.UUID, url string, threats ...string) {
	t.Helper()
	_, err := f.deps.ApplyVerdictForTest(context.Background(), id, f.teamID, url,
		scanning.Result{ThreatTypes: threats, ValidFor: 5 * time.Minute})
	require.NoError(t, err)
}

func TestGetLinkOmitsTheScanUntilTheDestinationIsChecked(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "ungeprueft", "https://example.org/ungeprueft")

	_, raw := getLinkView(t, f, created.ID)
	require.NotContains(t, raw, `"scan"`, "absent, not null: the schema promises an optional object")
}

func TestGetLinkReportsAFlag(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "gemeldet", "https://example.org/gemeldet")
	applyForTest(t, f, created.ID, created.DestinationURL, "SOCIAL_ENGINEERING")

	body, _ := getLinkView(t, f, created.ID)

	require.Equal(t, "flagged", body.State)
	require.NotNil(t, body.Scan)
	require.Equal(t, "flagged", body.Scan.Verdict)
	require.Equal(t, []string{"SOCIAL_ENGINEERING"}, body.Scan.ThreatTypes)
	require.NotNil(t, body.Scan.Since)
	require.False(t, body.Scan.CheckedAt.IsZero())
}

func TestGetLinkReportsACleanCheckWithoutASince(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "sauber", "https://example.org/sauber")
	applyForTest(t, f, created.ID, created.DestinationURL)

	body, raw := getLinkView(t, f, created.ID)

	require.NotNil(t, body.Scan)
	require.Equal(t, "clean", body.Scan.Verdict)
	require.Contains(t, raw, `"threat_types":[]`, "a list, never null")
	require.Nil(t, body.Scan.Since, "clean since its first check: no row says when it became clean")
	require.NotContains(t, raw, `"since"`, "absent, not null")
}

// A flag Google lifted leaves a clean row behind for the same destination, so
// the link can say when it became clean.
func TestGetLinkReportsWhenALiftedFlagBecameClean(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "entsperrt", "https://example.org/entsperrt")
	applyForTest(t, f, created.ID, created.DestinationURL, "MALWARE")
	applyForTest(t, f, created.ID, created.DestinationURL)

	body, raw := getLinkView(t, f, created.ID)

	require.Equal(t, "active", body.State)
	require.NotNil(t, body.Scan)
	require.Equal(t, "clean", body.Scan.Verdict)
	require.Contains(t, raw, `"threat_types":[]`, "the old threats do not outlive the flag")
	require.NotNil(t, body.Scan.Since)
}

// A verdict never describes a URL it did not see. The flag was lifted by the
// new destination, not by Google, so scan_checked_at still names the old
// check while scan_destination has been cleared: nothing has looked at the new
// URL yet, and the link must not claim a clean check for it.
func TestGetLinkDropsTheScanWhenTheDestinationChanges(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "umgezogen", "https://example.org/alt")
	applyForTest(t, f, created.ID, created.DestinationURL, "MALWARE")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/neu"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// The precondition that makes this the cleared-destination case rather
	// than the mismatched one below: the old check is still on record, but
	// the URL it judged has been forgotten.
	var checkedAtSet, destinationCleared bool
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select scan_checked_at is not null, scan_destination is null from link where id = $1`,
		created.ID).Scan(&checkedAtSet, &destinationCleared))
	require.True(t, checkedAtSet, "scan_checked_at keeps naming the old check")
	require.True(t, destinationCleared, "the lifted flag cleared scan_destination")

	body, raw := getLinkView(t, f, created.ID)
	require.Equal(t, "active", body.State)
	require.NotContains(t, raw, `"scan"`)
}

// The other way a destination change leaves a link unchecked: an active link
// keeps scan_destination at the URL it last judged, so the verdict is dropped
// because it names a different URL, not because anything was cleared. Until
// Google has looked at the new one the old clean check must not be reported
// for it, and once it has, the answer is the new check's.
func TestGetLinkDropsACleanScanForAnotherDestinationUntilItIsChecked(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "weitergezogen", "https://example.org/a")
	applyForTest(t, f, created.ID, created.DestinationURL)

	before, _ := getLinkView(t, f, created.ID)
	require.NotNil(t, before.Scan, "the check of A is reported while the link points at A")

	const newDestination = "https://example.org/b"
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": newDestination})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var destinationStillA bool
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select scan_destination = 'https://example.org/a' from link where id = $1`,
		created.ID).Scan(&destinationStillA))
	require.True(t, destinationStillA, "an active link keeps the URL its last check judged")

	body, raw := getLinkView(t, f, created.ID)
	require.Equal(t, "active", body.State)
	require.NotContains(t, raw, `"scan"`, "A's clean check says nothing about B")

	applyForTest(t, f, created.ID, newDestination)

	body, raw = getLinkView(t, f, created.ID)
	require.NotNil(t, body.Scan)
	require.Equal(t, "clean", body.Scan.Verdict)
	require.False(t, body.Scan.CheckedAt.IsZero())
	require.NotContains(t, raw, `"since"`, "no row says when B became clean")
}

func TestListLinksNeverCarriesTheScan(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "liste", "https://example.org/liste")
	applyForTest(t, f, created.ID, created.DestinationURL, "MALWARE")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/teams/"+f.teamID.String()+"/links", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), `"state":"flagged"`)
	require.NotContains(t, rec.Body.String(), `"scan"`)
}

// The generated document, not the Go: packages/api-client is generated from
// it, and CLAUDE.md's Huma nullability entry is about exactly the case where
// the two disagree. A bare tag on an object pointer would publish a required
// property that arrives as null.
func TestLinkSchemaPublishesTheScanAsAnOptionalObjectAndStateAsAnEnum(t *testing.T) {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)
	schemas := humaAPI.OpenAPI().Components.Schemas.Map()

	linkSchema := schemas["Link"]
	require.NotNil(t, linkSchema)
	require.NotContains(t, linkSchema.Required, "scan")
	scan := linkSchema.Properties["scan"]
	require.NotNil(t, scan)
	require.Equal(t, "#/components/schemas/LinkScan", scan.Ref)
	require.False(t, scan.Nullable)
	require.ElementsMatch(t, []any{"active", "disabled", "expired", "flagged"},
		linkSchema.Properties["state"].Enum)

	linkScan := schemas["LinkScan"]
	require.NotNil(t, linkScan)
	require.ElementsMatch(t, []string{"verdict", "threat_types", "checked_at"}, linkScan.Required)
	require.ElementsMatch(t, []any{"clean", "flagged"}, linkScan.Properties["verdict"].Enum)
}
