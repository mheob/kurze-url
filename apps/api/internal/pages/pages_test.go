package pages_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/pages"
)

func TestNegotiatePicksGermanWhenPreferred(t *testing.T) {
	require.Equal(t, pages.LocaleDE, pages.Negotiate("de-DE,de;q=0.9,en;q=0.8"))
	require.Equal(t, pages.LocaleDE, pages.Negotiate("de"))
}

func TestNegotiateDefaultsToEnglish(t *testing.T) {
	require.Equal(t, pages.LocaleEN, pages.Negotiate(""))
	require.Equal(t, pages.LocaleEN, pages.Negotiate("en-GB,en;q=0.9"))
	require.Equal(t, pages.LocaleEN, pages.Negotiate("fr-FR,fr;q=0.9"))
}

func TestNegotiateRespectsQualityOrder(t *testing.T) {
	require.Equal(t, pages.LocaleEN, pages.Negotiate("en;q=0.9,de;q=0.5"))
	require.Equal(t, pages.LocaleDE, pages.Negotiate("en;q=0.4,de;q=0.8"))
}

func TestRenderErrorWritesTheStatusAndLocalisedCopy(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderError(rec, http.StatusNotFound, pages.LocaleDE, pages.KindNotFound)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	body := rec.Body.String()
	require.Contains(t, body, `lang="de"`)
	require.Contains(t, body, "nicht gefunden")
}

func TestRenderErrorHasDistinctCopyPerKind(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range []pages.Kind{
		pages.KindNotFound, pages.KindDisabled, pages.KindExpired,
		pages.KindFlagged, pages.KindRateLimited, pages.KindServerError,
	} {
		rec := httptest.NewRecorder()
		pages.RenderError(rec, http.StatusOK, pages.LocaleEN, kind)
		body := rec.Body.String()
		require.False(t, seen[body], "kind %q reuses another kind's copy", kind)
		seen[body] = true
	}
}

func TestRenderPasswordPromptPostsToTheGivenAction(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderPasswordPrompt(rec, http.StatusOK, pages.LocaleEN, "/hello/verify", false)

	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, body, `method="post"`)
	require.Contains(t, body, `action="/hello/verify"`)
	require.Contains(t, body, `type="password"`)
	require.Contains(t, body, `name="password"`)
	require.NotContains(t, body, "incorrect")
}

func TestRenderPasswordPromptShowsAnErrorAfterAWrongAttempt(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderPasswordPrompt(rec, http.StatusUnauthorized, pages.LocaleEN, "/hello/verify", true)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, strings.ToLower(rec.Body.String()), "incorrect")
}

func TestRenderPasswordPromptEscapesTheAction(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderPasswordPrompt(rec, http.StatusOK, pages.LocaleEN, `/x"><script>alert(1)</script>/verify`, false)

	require.NotContains(t, rec.Body.String(), "<script>alert(1)</script>")
}

func TestPasswordPromptIsAccessible(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderPasswordPrompt(rec, http.StatusOK, pages.LocaleEN, "/hello/verify", true)

	body := rec.Body.String()
	require.Contains(t, body, `<label for="password"`, "the input needs a programmatic label")
	require.Contains(t, body, `id="password"`)
	require.Contains(t, body, `role="alert"`, "the error must be announced to screen readers")
	require.Contains(t, body, `autocomplete="current-password"`)
}
