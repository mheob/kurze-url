package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/link"
	"github.com/mheob/kurze-url/apps/api/internal/slug"
)

type linkBody struct {
	ID               uuid.UUID  `json:"id"`
	TeamID           uuid.UUID  `json:"team_id"`
	DomainID         uuid.UUID  `json:"domain_id"`
	Hostname         string     `json:"hostname"`
	Slug             string     `json:"slug"`
	ShortURL         string     `json:"short_url"`
	DestinationURL   string     `json:"destination_url"`
	RedirectType     int        `json:"redirect_type"`
	State            string     `json:"state"`
	ExpiresAt        *time.Time `json:"expires_at"`
	HasPassword      bool       `json:"has_password"`
	AnalyticsEnabled bool       `json:"analytics_enabled"`
	CreatedBy        uuid.UUID  `json:"created_by"`
}

func TestCreateLinkGeneratesASlugOnTheSharedDomain(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/sommerfest"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[linkBody](t, rec)

	require.Len(t, body.Slug, slug.Length)
	require.NoError(t, slug.Validate(body.Slug))
	require.Equal(t, f.sharedDomainID, body.DomainID, "no domain_id means the shared domain")
	require.Equal(t, f.teamID, body.TeamID)
	require.Equal(t, "active", body.State)
	require.Equal(t, 302, body.RedirectType)
	require.False(t, body.HasPassword)
	require.True(t, body.AnalyticsEnabled)
	require.Equal(t, f.members[authz.RoleEditor].id, body.CreatedBy)
	require.Contains(t, body.ShortURL, body.Slug)
	require.NotContains(t, rec.Body.String(), "password_hash")
}

func TestCreateLinkAcceptsACustomAliasAndLowercasesIt(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/jhv", "slug": "JHV-2026"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "jhv-2026", decode[linkBody](t, rec).Slug)
}

func TestCreateLinkRejectsATakenAliasWith409(t *testing.T) {
	f := newTenancyFixture(t)
	path := "/v1/teams/" + f.teamID.String() + "/links"
	body := map[string]any{"destination_url": "https://example.org/x", "slug": "sommerfest"}

	require.Equal(t, http.StatusCreated, f.do(t, f.members[authz.RoleEditor], http.MethodPost, path, body).Code)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost, path, body)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestCreateLinkRejectsAReservedAlias(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/x", "slug": "health"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"/health is the router's own path and must never become a link")
}

func TestCreateLinkRejectsANonHTTPSDestination(t *testing.T) {
	f := newTenancyFixture(t)

	for _, bad := range []string{
		"http://example.org", "javascript:alert(1)", "https://127.0.0.1/admin",
	} {
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
			"/v1/teams/"+f.teamID.String()+"/links",
			map[string]any{"destination_url": bad})
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%q must be refused", bad)
	}
}

func TestCreateLinkRejectsAPastExpiry(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"expires_at":      time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"creating an already-dead link is never intentional")
}

func TestCreateLinkRefusesAnotherTeamsDomain(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"domain_id":       other.teamDomainID.String(),
		})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.NotContains(t, rec.Body.String(), other.teamHostname,
		"the error must not disclose a hostname the caller does not own")
}

func TestCreateLinkClearsANegativeCacheEntry(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()

	// Somebody probed this slug before the link existed, so the redirect path
	// cached "no such link". Creating the link must clear that, or the new
	// link 404s for up to NotFoundCacheTTL for no visible reason.
	cacheKey := link.CacheKey(f.deps.SharedDomain.Hostname, "sommerfest")
	require.NoError(t, f.deps.Cache.PutNotFound(ctx, cacheKey, time.Minute))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/s", "slug": "sommerfest"})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	_, err := f.deps.Cache.Raw().Get(ctx, cacheKey).Result()
	require.Error(t, err, "the not-found sentinel must be gone after the link is created")
}

func TestCreateLinkWritesOneAuditRow(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/audit", "slug": "audited"})
	require.Equal(t, http.StatusCreated, rec.Code)
	created := decode[linkBody](t, rec)

	var count int
	var metadata string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*), coalesce(max(metadata::text), '') from audit_log
		 where team_id = $1 and action = 'link.created' and entity_id = $2`,
		f.teamID, created.ID).Scan(&count, &metadata))

	require.Equal(t, 1, count)
	require.Contains(t, metadata, "audited")
	require.NotContains(t, metadata, "password")
}

func TestCreateLinkIsRefusedForAViewer(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/x"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}
