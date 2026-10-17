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
		pages.KindRateLimited, pages.KindServerError, pages.KindUnavailable,
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

// Google's terms, not style: a qualified claim, Google's definition of the
// threat, the attribution, and the admission that Google can be wrong.
func TestRenderFlaggedQualifiesTheThreatAndCreditsGoogle(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderFlagged(rec, pages.LocaleEN, []string{"SOCIAL_ENGINEERING"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	body := rec.Body.String()
	require.Contains(t, body, "<h1>Suspected phishing site</h1>")
	require.Contains(t, body,
		`href="https://developers.google.com/search/docs/monitor-debug/security/social-engineering"`)
	require.Contains(t, body, `href="https://developers.google.com/safe-browsing/v4/advisory"`)
	require.Contains(t, body, "Advisory provided by Google")
	require.Contains(t, body, "cannot guarantee")
	require.NotContains(t, body, "<script", "the redirect surface's pages carry no JavaScript")
}

// Google's usage page defines malware and unwanted software on one page, so
// both threat types link there, each under its own label.
func TestRenderFlaggedLinksEveryReportedThreatToItsDefinition(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderFlagged(rec, pages.LocaleEN, []string{"UNWANTED_SOFTWARE", "MALWARE"})

	body := rec.Body.String()
	require.Contains(t, body, "<h1>Possibly harmful software</h1>")
	require.Contains(t, body, `href="https://developers.google.com/search/docs/monitor-debug/security/malware"`)
	require.Contains(t, body, "What Google means by malware")
	require.Contains(t, body, "What Google means by unwanted software")
	require.NotContains(t, body, "phishing")
}

// A threat type Google adds later still gets a qualified text and a link, and
// a mix of categories gets the generic heading over each category's text.
func TestRenderFlaggedUsesTheGenericHeadingForAMixOrAnUnknownThreat(t *testing.T) {
	for _, threats := range [][]string{
		{"SOCIAL_ENGINEERING", "MALWARE"},
		{"THREAT_TYPE_FROM_THE_FUTURE"},
		nil,
	} {
		rec := httptest.NewRecorder()
		pages.RenderFlagged(rec, pages.LocaleEN, threats)
		require.Contains(t, rec.Body.String(), "<h1>Suspected unsafe site</h1>", "threats %v", threats)
	}

	rec := httptest.NewRecorder()
	pages.RenderFlagged(rec, pages.LocaleEN, []string{"SOCIAL_ENGINEERING", "MALWARE"})
	require.Contains(t, rec.Body.String(), "phishing site")
	require.Contains(t, rec.Body.String(), "possibly harmful software")

	rec = httptest.NewRecorder()
	pages.RenderFlagged(rec, pages.LocaleEN, []string{"THREAT_TYPE_FROM_THE_FUTURE"})
	require.Contains(t, rec.Body.String(), `href="https://safebrowsing.google.com/"`)
}

// The German attribution is Google's own, from the German version of its
// usage page, not a translation of the English line.
func TestRenderFlaggedIsGerman(t *testing.T) {
	rec := httptest.NewRecorder()

	pages.RenderFlagged(rec, pages.LocaleDE, []string{"SOCIAL_ENGINEERING"})

	body := rec.Body.String()
	require.Contains(t, body, `lang="de"`)
	require.Contains(t, body, "Mutmaßliche Phishing-Seite")
	require.Contains(t, body, "Von Google bereitgestellte Hinweise")
	require.Contains(t, body, "nicht garantieren")
}

// The old copy said the link "was flagged as unsafe": absolute, without
// Google's attribution, and with no confirmation behind it. RenderError takes
// no threat types, so it cannot know that Google confirmed anything in the
// last thirty minutes; KindFlagged there gets the neutral page, and only
// RenderFlagged may show Google's warning.
func TestRenderErrorNeverShowsTheGoogleWarningForKindFlagged(t *testing.T) {
	for _, locale := range []pages.Locale{pages.LocaleEN, pages.LocaleDE} {
		flagged := httptest.NewRecorder()
		pages.RenderError(flagged, http.StatusServiceUnavailable, locale, pages.KindFlagged)
		neutral := httptest.NewRecorder()
		pages.RenderError(neutral, http.StatusServiceUnavailable, locale, pages.KindUnavailable)

		require.Equal(t, http.StatusServiceUnavailable, flagged.Code)
		require.Equal(t, neutral.Body.String(), flagged.Body.String(), "locale %s", locale)
		require.NotContains(t, flagged.Body.String(), "Google", "locale %s", locale)
	}
}

// Without a fresh confirmation the terms forbid calling the destination
// unsafe, so this page names neither a threat nor Google.
func TestTheUnavailablePageSaysNothingAboutTheDestination(t *testing.T) {
	for _, locale := range []pages.Locale{pages.LocaleEN, pages.LocaleDE} {
		rec := httptest.NewRecorder()

		pages.RenderError(rec, http.StatusServiceUnavailable, locale, pages.KindUnavailable)

		body := rec.Body.String()
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.NotContains(t, body, "Google", "locale %s", locale)
		require.NotContains(t, body, "unsafe", "locale %s", locale)
		require.NotContains(t, body, "unsicher", "locale %s", locale)
	}

	rec := httptest.NewRecorder()
	pages.RenderError(rec, http.StatusServiceUnavailable, pages.LocaleEN, pages.KindUnavailable)
	require.Contains(t, rec.Body.String(), "temporarily unavailable")

	rec = httptest.NewRecorder()
	pages.RenderError(rec, http.StatusServiceUnavailable, pages.LocaleDE, pages.KindUnavailable)
	require.Contains(t, rec.Body.String(), "vorübergehend nicht verfügbar")
}
