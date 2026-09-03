package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	FolderID         *uuid.UUID `json:"folder_id"`
	Tags             []tagBody  `json:"tags"`
	CreatedBy        uuid.UUID  `json:"created_by"`
}

// maxTagsPerLinkForTests mirrors maxTagsPerLink in internal/api/limits.go.
// This file runs in api_test, not api, so it cannot see that unexported
// constant directly; a drift between the two would only ever loosen this
// test (the cap enforcement itself is exercised against whatever the real
// constant says), the same tradeoff folderCapForTests and tagCapForTests make.
const maxTagsPerLinkForTests = 10

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

func (f *tenancyFixture) createLink(t *testing.T, slug, dest string) linkBody {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": dest, "slug": slug})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	return decode[linkBody](t, rec)
}

type linkPage struct {
	Items      []linkBody `json:"items"`
	Page       int        `json:"page"`
	PerPage    int        `json:"per_page"`
	TotalCount int        `json:"total_count"`
}

func TestListLinksReturnsThePageEnvelope(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "eins", "https://example.org/eins")
	f.createLink(t, "zwei", "https://example.org/zwei")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	page := decode[linkPage](t, rec)

	// The fixture itself seeds one link, so two created here makes three.
	require.Equal(t, 3, page.TotalCount)
	require.Len(t, page.Items, 3)
	require.Equal(t, 1, page.Page)
	for _, item := range page.Items {
		require.Equal(t, f.teamID, item.TeamID)
		require.NotEmpty(t, item.ShortURL)
	}
}

func TestListLinksDefaultsToNewestFirst(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "aelter", "https://example.org/a")
	time.Sleep(10 * time.Millisecond)
	newest := f.createLink(t, "neuer", "https://example.org/b")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links", nil)

	require.Equal(t, newest.ID, decode[linkPage](t, rec).Items[0].ID)
}

func TestListLinksSortsAscendingOnRequest(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "aelter", "https://example.org/a")
	time.Sleep(10 * time.Millisecond)
	f.createLink(t, "neuer", "https://example.org/b")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?sort=created_at", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "fixture", decode[linkPage](t, rec).Items[0].Slug,
		"ascending starts with the fixture's own, oldest link")
}

func TestListLinksFiltersBySubstring(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "sommerfest", "https://example.org/sommer")
	f.createLink(t, "winterfeier", "https://example.org/winter")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?q=sommer", nil)

	page := decode[linkPage](t, rec)
	require.Len(t, page.Items, 1)
	require.Equal(t, "sommerfest", page.Items[0].Slug)
}

func TestListLinksFiltersByDomain(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "aufshared", "https://example.org/s")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?domain_id="+f.sharedDomainID.String(), nil)

	page := decode[linkPage](t, rec)
	require.Len(t, page.Items, 1, "the fixture's own link is on the team domain, not the shared one")
	require.Equal(t, "aufshared", page.Items[0].Slug)
}

func TestListLinksRejectsAnUnknownSort(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?sort=clicks", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"click-count sorting needs a join this plan does not build; refuse rather than ignore")
}

func TestListLinksNeverShowsAnotherTeamsLinks(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	other.createLink(t, "geheim", "https://example.org/geheim")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?per_page=100", nil)

	for _, item := range decode[linkPage](t, rec).Items {
		require.Equal(t, f.teamID, item.TeamID)
		require.NotEqual(t, "geheim", item.Slug)
	}
}

func TestListLinksReportsATotalPastTheLastPage(t *testing.T) {
	f := newTenancyFixture(t)
	f.createLink(t, "eins", "https://example.org/eins")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?page=9&per_page=10", nil)

	page := decode[linkPage](t, rec)
	require.Empty(t, page.Items)
	require.Equal(t, 2, page.TotalCount,
		"a page past the end still has to report how many there are")
}

func TestListLinksRejectsAMalformedDomainFilter(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/links?domain_id=not-a-uuid", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestGetLinkReturnsTheLink(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "lesen", "https://example.org/lesen")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+created.ID.String(), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, created.ID, decode[linkBody](t, rec).ID)
	require.NotContains(t, rec.Body.String(), "password_hash")
}

func TestGetLinkIs404ForAnUnknownID(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+uuid.NewString(), nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetLinkIs422ForAMalformedID(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/not-a-uuid", nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestUpdateLinkChangesTheDestinationAndInvalidatesTheCache(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "aendern", "https://example.org/alt")

	// Warm the cache the way a real visit would.
	cacheKey := link.CacheKey(created.Hostname, created.Slug)
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{
		ID: created.ID, TeamID: created.TeamID, DestinationURL: created.DestinationURL,
		RedirectType: 302, State: "active", AnalyticsEnabled: true,
	}, time.Hour))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/neu"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "https://example.org/neu", decode[linkBody](t, rec).DestinationURL)

	_, err := f.deps.Cache.Raw().Get(ctx, cacheKey).Result()
	require.Error(t, err,
		"a 302 promises destination changes take effect immediately, not after LinkCacheTTL")
}

func TestUpdateLinkChangingTheSlugInvalidatesBothKeys(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "alt", "https://example.org/x")

	oldKey := link.CacheKey(created.Hostname, "alt")
	newKey := link.CacheKey(created.Hostname, "neu")
	require.NoError(t, f.deps.Cache.PutLink(ctx, oldKey, link.Cached{ID: created.ID}, time.Hour))
	require.NoError(t, f.deps.Cache.PutNotFound(ctx, newKey, time.Minute))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"slug": "NEU"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "neu", decode[linkBody](t, rec).Slug)

	_, err := f.deps.Cache.Raw().Get(ctx, oldKey).Result()
	require.Error(t, err, "the old slug must stop resolving")
	_, err = f.deps.Cache.Raw().Get(ctx, newKey).Result()
	require.Error(t, err, "the new slug's cached not-found sentinel must be cleared too")
}

func TestUpdateLinkRefusesAPasswordField(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "kennwort", "https://example.org/x")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"password": "hunter2"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"passwords have their own endpoint, their own audit action and a tighter rate limit")
}

func TestUpdateLinkRefusesMovingItToAnotherDomain(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "umziehen", "https://example.org/x")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"domain_id": f.teamDomainID.String()})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"moving a link changes its short URL, breaking every printed copy of it")
}

func TestUpdateLinkAcceptsOnlyActiveAndDisabled(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "zustand", "https://example.org/x")
	path := "/v1/links/" + created.ID.String()

	require.Equal(t, http.StatusOK,
		f.do(t, f.members[authz.RoleEditor], http.MethodPatch, path,
			map[string]any{"state": "disabled"}).Code)

	for _, systemState := range []string{"flagged", "expired"} {
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, path,
			map[string]any{"state": systemState})
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
			"%q is set by the system, never by a caller", systemState)
	}
}

func TestUpdateLinkWritesOneAuditRowNamingWhatChanged(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "protokoll", "https://example.org/alt")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/neu", "redirect_type": 301})
	require.Equal(t, http.StatusOK, rec.Code)

	var count int
	var metadata string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*), coalesce(max(metadata::text), '') from audit_log
		 where team_id = $1 and action = 'link.updated' and entity_id = $2`,
		f.teamID, created.ID).Scan(&count, &metadata))

	require.Equal(t, 1, count, "one PATCH is one audit row, however many fields it touched")
	require.Contains(t, metadata, "destination_url")
	require.Contains(t, metadata, "redirect_type")
	require.Contains(t, metadata, "changed")
	require.NotContains(t, metadata, "slug", "a field the request did not change must not be listed")
}

func TestUpdateLinkAuditRecordsBothHalvesOfExpiresAt(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "ablaufdatum", "https://example.org/ablauf")
	require.Nil(t, created.ExpiresAt, "the fixture link starts with no expiry")

	future := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"expires_at": future.Format(time.RFC3339)})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var raw []byte
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select metadata from audit_log
		 where team_id = $1 and action = 'link.updated' and entity_id = $2`,
		f.teamID, created.ID).Scan(&raw))

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(raw, &metadata))

	expiresAt, ok := metadata["expires_at"].(map[string]any)
	require.True(t, ok, "expires_at metadata: %s", raw)

	// Map keys, not just decoded values: an absent "from" key and an explicit
	// JSON null both decode to a nil interface, so checking presence is the
	// only way to prove the key was actually written, matching its siblings.
	fromValue, hasFrom := expiresAt["from"]
	require.True(t, hasFrom, "expires_at.from must be present, siblings all write it: %s", raw)
	require.Nil(t, fromValue,
		"from must be null for a link that had no expiry before this PATCH")

	toValue, hasTo := expiresAt["to"]
	require.True(t, hasTo, "expires_at.to must be present: %s", raw)
	toTime, err := time.Parse(time.RFC3339, fmt.Sprint(toValue))
	require.NoError(t, err)
	require.WithinDuration(t, future, toTime, time.Second)
}

func TestUpdateLinkWritesNoAuditRowWhenNothingChanged(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "unveraendert", "https://example.org/gleich")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{"destination_url": "https://example.org/gleich"})
	require.Equal(t, http.StatusOK, rec.Code)

	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = 'link.updated' and entity_id = $1`,
		created.ID).Scan(&count))
	require.Zero(t, count, "a no-op PATCH must not write a misleading audit entry")
}

func TestDeleteLinkRemovesItAndTheCacheEntry(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "weg", "https://example.org/weg")

	cacheKey := link.CacheKey(created.Hostname, created.Slug)
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{ID: created.ID}, time.Hour))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete, "/v1/links/"+created.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.Equal(t, http.StatusNotFound,
		f.do(t, f.members[authz.RoleViewer], http.MethodGet, "/v1/links/"+created.ID.String(), nil).Code)

	_, err := f.deps.Cache.Raw().Get(ctx, cacheKey).Result()
	require.Error(t, err, "a deleted link must stop resolving immediately")

	var count int
	require.NoError(t, f.pool.QueryRow(ctx,
		`select count(*) from audit_log where action = 'link.deleted' and entity_id = $1`,
		created.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestUpdateAndDeleteAreRefusedForAViewer(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "nurlesen", "https://example.org/x")
	path := "/v1/links/" + created.ID.String()

	require.Equal(t, http.StatusForbidden,
		f.do(t, f.members[authz.RoleViewer], http.MethodPatch, path,
			map[string]any{"state": "disabled"}).Code)
	require.Equal(t, http.StatusForbidden,
		f.do(t, f.members[authz.RoleViewer], http.MethodDelete, path, nil).Code)
}

func TestCreateLinkFilesItInAFolder(t *testing.T) {
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/fest",
			"folder_id":       folder.ID.String(),
		})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[linkBody](t, rec)
	require.NotNil(t, body.FolderID)
	require.Equal(t, folder.ID, *body.FolderID)
}

func TestCreateLinkRejectsAnotherTeamsFolder(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	foreign := other.createFolder(t, "Fremd")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"folder_id":       foreign.ID.String(),
		})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())

	// The fixture itself seeds one link ("fixture"); nothing else must exist.
	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from link where team_id = $1`, f.teamID).Scan(&count))
	require.Equal(t, 1, count, "no link must have been created")
}

// normalizeID replaces every occurrence of id's text in body with a fixed
// placeholder, so responses that legitimately embed different ids can still
// be compared for everything *but* the id.
func normalizeID(body string, id uuid.UUID) string {
	return strings.ReplaceAll(body, id.String(), "<id>")
}

// TestAnotherTeamsFolderIsIndistinguishableFromAMissingOne asserts the
// property the security review actually calls for: for any given folder_id,
// the response must not reveal whether that id exists in another team or
// does not exist at all. It does NOT assert that the two responses are
// byte-identical — they can't be, since foreign and missingID are two
// different ids and the message deliberately names the id it was given
// (resolveFolderRef's doc comment explains why: tag_ids can carry up to ten
// entries, and a client needs to know which one was rejected). What must
// never vary is the message *template* around the id. If the not-found case
// used a different template than the wrong-team case — e.g. "no such folder"
// vs. "that folder belongs to another team" — an attacker could submit a
// candidate id and learn from the wording alone whether it exists at all,
// which is exactly the leak this test guards against. Substituting each
// response's own id for a fixed placeholder before comparing proves the
// template is shared without demanding the impossible.
func TestAnotherTeamsFolderIsIndistinguishableFromAMissingOne(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	foreign := other.createFolder(t, "Fremd")
	missingID := uuid.New()

	foreignResp := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"folder_id":       foreign.ID.String(),
		})
	missingResp := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"folder_id":       missingID.String(),
		})

	require.Equal(t, foreignResp.Code, missingResp.Code, "status must not differ")
	require.Equal(t,
		normalizeID(foreignResp.Body.String(), foreign.ID),
		normalizeID(missingResp.Body.String(), missingID),
		"message template must not differ, once each response's own id is normalized away")
}

func TestCreateLinkAttachesTags(t *testing.T) {
	f := newTenancyFixture(t)
	presse := f.createTag(t, "Presse")
	fest := f.createTag(t, "Sommerfest")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/pm",
			"tag_ids":         []string{presse.ID.String(), fest.ID.String()},
		})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[linkBody](t, rec)
	require.Len(t, body.Tags, 2)
	// Tags come back ordered by name, so the response is stable.
	require.Equal(t, "Presse", body.Tags[0].Name)
	require.Equal(t, "Sommerfest", body.Tags[1].Name)
}

func TestCreateLinkRejectsAnotherTeamsTag(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	foreign := other.createTag(t, "Fremd")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"tag_ids":         []string{foreign.ID.String()},
		})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())

	var linkCount int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from link where team_id = $1`, f.teamID).Scan(&linkCount))
	require.Equal(t, 1, linkCount, "no link must have been created")

	var joins int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from link_tag where tag_id = $1`, foreign.ID).Scan(&joins))
	require.Zero(t, joins, "no link_tag row must have been created")
}

// TestAnotherTeamsTagIsIndistinguishableFromAMissingOne is the tag_ids
// equivalent of TestAnotherTeamsFolderIsIndistinguishableFromAMissingOne
// above: same normalized-template property (see that test's comment for the
// full reasoning), applied to resolveTagRefs instead of resolveFolderRef.
func TestAnotherTeamsTagIsIndistinguishableFromAMissingOne(t *testing.T) {
	f := newTenancyFixture(t)
	other := newTenancyFixture(t)
	foreign := other.createTag(t, "Fremd")
	missingID := uuid.New()

	foreignResp := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"tag_ids":         []string{foreign.ID.String()},
		})
	missingResp := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"tag_ids":         []string{missingID.String()},
		})

	require.Equal(t, foreignResp.Code, missingResp.Code, "status must not differ")
	require.Equal(t,
		normalizeID(foreignResp.Body.String(), foreign.ID),
		normalizeID(missingResp.Body.String(), missingID),
		"message template must not differ, once each response's own id is normalized away")
}

func TestUpdateLinkReplacesTheWholeTagSet(t *testing.T) {
	f := newTenancyFixture(t)
	a := f.createTag(t, "Alpha")
	b := f.createTag(t, "Beta")
	c := f.createTag(t, "Gamma")
	linkID := f.createLinkWithTags(t, "https://example.org/x", a.ID, b.ID)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+linkID.String(),
		map[string]any{"tag_ids": []string{a.ID.String(), c.ID.String()}})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[linkBody](t, rec)
	require.Len(t, body.Tags, 2)
	// Tags come back ordered by name, so the response is stable.
	require.Equal(t, "Alpha", body.Tags[0].Name)
	require.Equal(t, "Gamma", body.Tags[1].Name)
}

func TestUpdateLinkWithEmptyTagIDsDetachesEverything(t *testing.T) {
	f := newTenancyFixture(t)
	tag := f.createTag(t, "Presse")
	linkID := f.createLinkWithTags(t, "https://example.org/x", tag.ID)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+linkID.String(),
		map[string]any{"tag_ids": []string{}})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var joins int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from link_tag where link_id = $1`, linkID).Scan(&joins))
	require.Zero(t, joins)
}

func TestUpdateLinkWithoutTagIDsLeavesTagsAlone(t *testing.T) {
	f := newTenancyFixture(t)
	tag := f.createTag(t, "Presse")
	linkID := f.createLinkWithTags(t, "https://example.org/x", tag.ID)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+linkID.String(),
		map[string]any{"destination_url": "https://example.org/y"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var joins int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from link_tag where link_id = $1`, linkID).Scan(&joins))
	require.Equal(t, 1, joins, "a PATCH that never mentions tag_ids must leave tags untouched")
}

func TestUpdateLinkWithNullFolderIDUnfilesIt(t *testing.T) {
	// The distinction this asserts is why the handler reads RawBody: an
	// omitted folder_id and an explicit null are both a nil pointer.
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest")
	linkID := f.createLinkInFolder(t, "https://example.org/x", folder.ID)

	rec := f.doRaw(t, f.members[authz.RoleEditor], http.MethodPatch,
		"/v1/links/"+linkID.String(), `{"folder_id": null}`)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Nil(t, decode[linkBody](t, rec).FolderID)
}

func TestUpdateLinkWithoutFolderIDLeavesItFiled(t *testing.T) {
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest")
	linkID := f.createLinkInFolder(t, "https://example.org/x", folder.ID)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+linkID.String(),
		map[string]any{"destination_url": "https://example.org/y"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[linkBody](t, rec)
	require.NotNil(t, body.FolderID, "a PATCH that never mentions folder_id must leave it filed")
	require.Equal(t, folder.ID, *body.FolderID)
}

func TestUpdateLinkRecordsFolderAndTagChangesInOneAuditRow(t *testing.T) {
	// One row per PATCH, not one per changed field: a single request is a
	// single request, and metadata.changed says which fields moved.
	f := newTenancyFixture(t)
	folder := f.createFolder(t, "Sommerfest")
	tag := f.createTag(t, "Presse")
	created := f.createLink(t, "audit-folder-tags", "https://example.org/x")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{
			"folder_id": folder.ID.String(),
			"tag_ids":   []string{tag.ID.String()},
		})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var rows int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from audit_log where entity_id = $1 and action = 'link.updated'`,
		created.ID).Scan(&rows))
	require.Equal(t, 1, rows, "one PATCH is one audit row, however many fields it touched")

	var changed []string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select array(select jsonb_array_elements_text(metadata->'changed'))
		 from audit_log where entity_id = $1 and action = 'link.updated'`,
		created.ID).Scan(&changed))
	require.Contains(t, changed, "folder_id")
	require.Contains(t, changed, "tags")
}

// TestUpdateLinkChangingOnlyFolderOrTagsLeavesTheRedirectCacheAlone proves the
// hot-path constraint by exercising it, not only by reading the code: neither
// folder_id nor tag_ids appears in link.Cached, so a PATCH that touches only
// those must not cost a single Redis command on the redirect path.
func TestUpdateLinkChangingOnlyFolderOrTagsLeavesTheRedirectCacheAlone(t *testing.T) {
	f := newTenancyFixture(t)
	ctx := context.Background()
	created := f.createLink(t, "nur-organisatorisch", "https://example.org/x")
	folder := f.createFolder(t, "Sommerfest")
	tag := f.createTag(t, "Presse")

	cacheKey := link.CacheKey(created.Hostname, created.Slug)
	require.NoError(t, f.deps.Cache.PutLink(ctx, cacheKey, link.Cached{
		ID: created.ID, TeamID: created.TeamID, DestinationURL: created.DestinationURL,
		RedirectType: 302, State: "active", AnalyticsEnabled: true,
	}, time.Hour))

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPatch, "/v1/links/"+created.ID.String(),
		map[string]any{
			"folder_id": folder.ID.String(),
			"tag_ids":   []string{tag.ID.String()},
		})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	_, err := f.deps.Cache.Raw().Get(ctx, cacheKey).Result()
	require.NoError(t, err,
		"an organizational-only change must not invalidate the redirect cache")
}

func TestCreateLinkEnforcesTheTagsPerLinkCap(t *testing.T) {
	f := newTenancyFixture(t)

	ids := make([]string, 0, maxTagsPerLinkForTests+1)
	for i := range maxTagsPerLinkForTests + 1 {
		tag := f.createTag(t, fmt.Sprintf("tag-%02d", i))
		ids = append(ids, tag.ID.String())
	}

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{
			"destination_url": "https://example.org/x",
			"tag_ids":         ids,
		})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
}
