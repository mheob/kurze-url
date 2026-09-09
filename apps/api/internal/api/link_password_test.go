package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
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
