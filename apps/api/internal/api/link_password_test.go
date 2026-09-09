package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/cache"
)

// countAuditActions is the same direct query links_test.go uses for
// link.updated, parameterized because this file asserts three different
// actions on the same entity.
func countAuditActions(t *testing.T, f *tenancyFixture, action string, entityID uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = $1 and entity_id = $2`,
		action, entityID).Scan(&count))
	return count
}

func TestSetLinkPasswordProtectsTheLink(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "mitglieder", "https://example.org/mitglieder")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, decode[linkBody](t, rec).HasPassword,
		"the response must carry has_password so the client needs no refetch")
}

// TestSetLinkPasswordKeepsTheLinksTags pins the same rule
// TestUpdateLinkWithoutTagIDsLeavesTagsAlone pins for PATCH: linkResponse
// defaults Tags to [], so any handler returning a full Link must call
// attachTags itself or a tagged link reports "tags": [] the moment its
// password changes. f.createLink makes an untagged link, which is why none
// of the other tests here would catch this — Tags: [] is accidentally
// correct for them.
func TestSetLinkPasswordKeepsTheLinksTags(t *testing.T) {
	f := newTenancyFixture(t)
	tag := f.createTag(t, "Presse")
	linkID := f.createLinkWithTags(t, "https://example.org/x", tag.ID)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+linkID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[linkBody](t, rec)
	require.Len(t, body.Tags, 1, "response must report the link's actual tags, not []")
	require.Equal(t, tag.ID, body.Tags[0].ID)
	require.Equal(t, "Presse", body.Tags[0].Name)
}

// TestSetLinkPasswordIsRateLimited pins allowPasswordSet the same way
// TestClaimDomainIsRateLimited (domains_test.go) pins allowDomainClaim: before
// this, allowPasswordSet was the only rate limiter in the codebase with no
// test driving it to refusal at all.
func TestSetLinkPasswordIsRateLimited(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.PasswordSetRateLimitPerHour = 1
	f.rebuildRouter()
	created := f.createLink(t, "ratelimited", "https://example.org/ratelimited")

	first := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body.String())

	second := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Bratkartoffeln!9"})
	require.Equal(t, http.StatusTooManyRequests, second.Code, "body: %s", second.Body.String())
}

// TestSetLinkPasswordRateLimitDisabledAtZero pins Ruling D from the SDD
// ledger: RATE_LIMIT_PASSWORD_SET_PER_HOUR=0 means "not enforced," the same
// as every other per-subject limit except RATE_LIMIT_INVITE_GLOBAL_PER_MONTH,
// which refuses everything at 0 instead. Nothing else in this suite drives
// the value to zero, so nothing else would catch a regression here.
func TestSetLinkPasswordRateLimitDisabledAtZero(t *testing.T) {
	f := newTenancyFixture(t)
	f.deps.Config.PasswordSetRateLimitPerHour = 0
	f.rebuildRouter()
	created := f.createLink(t, "unlimited", "https://example.org/unlimited")

	for i, password := range []string{"Kartoffelsalat!7", "Bratkartoffeln!9", "Zwiebelkuchen!3"} {
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
			"/v1/links/"+created.ID.String()+"/password",
			map[string]any{"password": password})
		require.Equal(t, http.StatusOK, rec.Code, "call %d, body: %s", i+1, rec.Body.String())
	}
}

// TestSetLinkPasswordRateLimitFailsClosed pins the divergence
// allowPasswordSet's own comment states: unlike allowLinkCreate, which logs
// and allows a Redis error through, allowPasswordSet must refuse the
// request. The broken client points at an address nothing listens on;
// cache.New never dials eagerly, so this only fails once Allow actually runs
// the script.
func TestSetLinkPasswordRateLimitFailsClosed(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "brokenredis", "https://example.org/brokenredis")

	broken, err := cache.New("redis://127.0.0.1:1/0")
	require.NoError(t, err)
	f.deps.Cache = broken
	f.rebuildRouter()

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})

	require.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
}

func TestSetLinkPasswordIsRefusedBelowEditor(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "nurlesen", "https://example.org/nurlesen")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestSetLinkPasswordRejectsAContextDerivedPassword pins the wire contract the
// frontend depends on: the reason travels as a typed ErrorDetail keyed by the
// field, never inside the prose, so a reworded message cannot break it. The
// link's own slug is the context source here — the simplest one to control
// from a test, since the fixture's team name is not.
func TestSetLinkPasswordRejectsAContextDerivedPassword(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "sommerfest", "https://example.org/sommerfest")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "sommerfest2026"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), `"location":"body.password"`)
	require.Contains(t, rec.Body.String(), `"value":"derived_from_context"`)
}

// TestSetLinkPasswordInvalidatesTheRedirectCache is the test that matters most
// in this whole plan, and the reason it drives HandleRedirect rather than
// spying on invalidateLink. link.Cached carries HasPassword, so a freshly
// protected link keeps redirecting straight through until the entry expires —
// up to LinkCacheTTL, one hour. That failure is completely silent: the API
// answers 200, the audit log records the change, the dashboard shows a
// protected link, and visitors sail past.
func TestSetLinkPasswordInvalidatesTheRedirectCache(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "geschuetzt", "https://example.org/geschuetzt")

	// Warm the cache: this redirect populates the entry that must be dropped.
	warm := f.redirect(t, created.Hostname, created.Slug)
	require.Equal(t, http.StatusFound, warm.Code)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	after := f.redirect(t, created.Hostname, created.Slug)
	require.Equal(t, http.StatusOK, after.Code,
		"a protected link must render the interstitial, not redirect")
	require.Empty(t, after.Header().Get("Location"))
}

func TestSetLinkPasswordAuditsSetThenChanged(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "zweimal", "https://example.org/zweimal")

	for _, password := range []string{"Kartoffelsalat!7", "Bratkartoffeln!9"} {
		rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
			"/v1/links/"+created.ID.String()+"/password",
			map[string]any{"password": password})
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	}

	require.Equal(t, 1, countAuditActions(t, f, "link.password_set", created.ID))
	require.Equal(t, 1, countAuditActions(t, f, "link.password_changed", created.ID),
		"the second write must be distinguishable from the first in the log")
}

// TestSetLinkPasswordWritesNoMetadata pins the audit-hygiene rule doc 05 set
// out: the log records that a password changed, never anything about its
// value. audit.ErrForbiddenMetadata refuses a plaintext or a hash, but not an
// empty map, so this is what stops someone adding a well-meant field later.
func TestSetLinkPasswordWritesNoMetadata(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "leer", "https://example.org/leer")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var metadata string
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`select coalesce(metadata::text, '') from audit_log
		 where action = 'link.password_set' and entity_id = $1`,
		created.ID).Scan(&metadata))
	require.NotContains(t, metadata, "Kartoffelsalat")
	require.NotContains(t, metadata, "argon2")
}

func TestRemoveLinkPasswordUnprotectsTheLink(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "wiederfrei", "https://example.org/wiederfrei")

	set := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, set.Code, "body: %s", set.Body.String())

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/links/"+created.ID.String()+"/password", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.False(t, decode[linkBody](t, rec).HasPassword)
	require.Equal(t, 1, countAuditActions(t, f, "link.password_removed", created.ID))
}

// TestRemoveLinkPasswordKeepsTheLinksTags pins the same rule
// TestSetLinkPasswordKeepsTheLinksTags pins for PUT: linkResponse defaults
// Tags to [], so removeLinkPassword must call attachTags itself or a tagged
// link reports "tags": [] the moment its password is removed. f.createLink
// makes an untagged link, which is why the other remove tests here would not
// catch this — Tags: [] is accidentally correct for them.
func TestRemoveLinkPasswordKeepsTheLinksTags(t *testing.T) {
	f := newTenancyFixture(t)
	tag := f.createTag(t, "Presse")
	linkID := f.createLinkWithTags(t, "https://example.org/x", tag.ID)

	set := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+linkID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, set.Code, "body: %s", set.Body.String())

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/links/"+linkID.String()+"/password", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[linkBody](t, rec)
	require.Len(t, body.Tags, 1, "response must report the link's actual tags, not []")
	require.Equal(t, tag.ID, body.Tags[0].ID)
	require.Equal(t, "Presse", body.Tags[0].Name)
}

// TestRemoveLinkPasswordIsIdempotent pins the choice the spec made: DELETE on
// a link that has no password answers 200 with the link unchanged. A 404 there
// would say nothing the caller does not already know while forcing every
// client to special-case it.
func TestRemoveLinkPasswordIsIdempotent(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "niegeschuetzt", "https://example.org/niegeschuetzt")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/links/"+created.ID.String()+"/password", nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.False(t, decode[linkBody](t, rec).HasPassword)
}

func TestRemoveLinkPasswordIsRefusedBelowEditor(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "nichtentfernen", "https://example.org/nichtentfernen")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodDelete,
		"/v1/links/"+created.ID.String()+"/password", nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestRemoveLinkPasswordInvalidatesTheRedirectCache is the mirror of the set
// case and fails the other way round: without invalidation a visitor keeps
// being asked for a password the Verein has already withdrawn.
func TestRemoveLinkPasswordInvalidatesTheRedirectCache(t *testing.T) {
	f := newTenancyFixture(t)
	created := f.createLink(t, "cachefrei", "https://example.org/cachefrei")

	set := f.do(t, f.members[authz.RoleEditor], http.MethodPut,
		"/v1/links/"+created.ID.String()+"/password",
		map[string]any{"password": "Kartoffelsalat!7"})
	require.Equal(t, http.StatusOK, set.Code, "body: %s", set.Body.String())

	warm := f.redirect(t, created.Hostname, created.Slug)
	require.Equal(t, http.StatusOK, warm.Code, "the interstitial must be cached first")

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodDelete,
		"/v1/links/"+created.ID.String()+"/password", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	after := f.redirect(t, created.Hostname, created.Slug)
	require.Equal(t, http.StatusFound, after.Code,
		"an unprotected link must redirect again, not keep asking for a password")
}
