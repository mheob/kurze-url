package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

// newTestApp builds the App a command would get from the harness.
func newTestApp(t *testing.T, h *harness) *App {
	t.Helper()
	a, err := newApp(h.deps)
	require.NoError(t, err)
	return a
}

func TestNewAppWiresTheEnvironmentOverrides(t *testing.T) {
	h := newHarness(t)

	a := newTestApp(t, h)

	require.Equal(t, h.api.URL, a.Env.APIURL)
	require.Equal(t, h.auth.URL, a.Env.SupabaseURL)
	require.Equal(t, testClientID, a.Env.ClientID)
	require.Equal(t, i18n.EN, a.Cat.Lang())
	require.Same(t, h.store, a.Store)
	require.NotNil(t, a.Config)
	require.NotNil(t, a.Session)
	require.NotNil(t, a.API)
	// The store is keyed by the Supabase URL's name, so a production login
	// and a preview login cannot overwrite each other.
	require.Equal(t, a.Env.Name, h.storeEnvName)
	require.Equal(t, h.deps.CredentialsDir, h.storeDir)
}

func TestNewAppTakesTheLanguageFromTheConfigBeforeTheEnvironment(t *testing.T) {
	h := newHarness(t)
	h.env["LANG"] = "en_US.UTF-8"
	h.writeConfig("language: de\n")

	require.Equal(t, i18n.DE, newTestApp(t, h).Cat.Lang())
}

func TestNewAppRefusesABrokenConfigWithoutBuildingAnything(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("language: fr\n")

	a, err := newApp(h.deps)

	require.Nil(t, a)
	require.Error(t, err)
	require.Empty(t, h.storeEnvName, "the credential store is not touched when the config is refused")
}

func TestNewAppRefusesABadEnvironmentOverride(t *testing.T) {
	h := newHarness(t)
	h.env["KURZE_URL_API_URL"] = "not a url"

	a, err := newApp(h.deps)

	require.Nil(t, a)
	require.ErrorContains(t, err, "KURZE_URL_API_URL")
}

func TestNewAppSendsTheBypassHeaderAndAUserAgent(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.env["KURZE_URL_VERCEL_BYPASS"] = "bypass-secret"
	h.serveMe(teamGruenwald)

	require.Equal(t, 0, h.run("whoami"), h.stderr.String())

	calls := h.api.callsTo(http.MethodGet, apiclient.PathMe)
	require.Len(t, calls, 1)
	require.True(t, calls[0].Header.Get("x-vercel-protection-bypass") == "bypass-secret",
		"the bypass header was not sent")
	require.Equal(t, "kurze-url/1.2.3", calls[0].Header.Get("User-Agent"))
	require.NotContains(t, h.stderr.String(), "bypass-secret")
	require.NotContains(t, h.stdout.String(), "bypass-secret")
}

// The session's refresh and the login both need the authorization server's
// endpoints, and they do not move between two calls seconds apart.
func TestDiscoveryRunsOncePerProcess(t *testing.T) {
	h := newHarness(t)
	a := newTestApp(t, h)

	first, err := a.discover(context.Background())
	require.NoError(t, err)
	second, err := a.discover(context.Background())
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, h.auth.URL+stubTokenPath, first.Token)
	require.Len(t, h.auth.callsTo(stubDiscoveryPath), 1)
}

func TestMeIsFetchedOnce(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("teams:\n  old-team:\n    folder: x\n")
	h.serveMe(teamGruenwald)
	a := newTestApp(t, h)

	first, err := a.me(context.Background())
	require.NoError(t, err)
	second, err := a.me(context.Background())
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, "ada@example.org", first.Email)
	require.Len(t, h.api.callsTo(http.MethodGet, apiclient.PathMe), 1)
	require.Equal(t, 1, strings.Count(h.stderr.String(), "old-team"),
		"the warning is printed on the first call and not again")
}

func TestMeWarnsForEveryStaleSection(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("teams:\n  old-team:\n    folder: x\n  sv-gruenwald:\n    folder: y\n  other-team:\n    folder: z\n")
	h.serveMe(teamGruenwald)
	a := newTestApp(t, h)

	_, err := a.me(context.Background())

	require.NoError(t, err)
	require.Contains(t, h.stderr.String(), "“old-team”")
	require.Contains(t, h.stderr.String(), "“other-team”")
	require.NotContains(t, h.stderr.String(), "“sv-gruenwald”")
	require.Less(t, strings.Index(h.stderr.String(), "old-team"), strings.Index(h.stderr.String(), "other-team"),
		"warnings follow the file's order")
}

func TestMeDoesNotCacheAFailure(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.api.problem(http.MethodGet, apiclient.PathMe, http.StatusInternalServerError, "boom")
	a := newTestApp(t, h)

	_, err := a.me(context.Background())
	require.Error(t, err)

	h.serveMe(teamGruenwald)
	me, err := a.me(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ada@example.org", me.Email)
}

func TestResolveTeam(t *testing.T) {
	tests := []struct {
		name   string
		config string
		flag   string
		want   string // the slug, or "" for an error
		err    string
	}{
		{name: "the flag", flag: "tc-muster", want: "tc-muster"},
		{name: "the configured default", config: "team: sv-gruenwald\n", want: "sv-gruenwald"},
		{name: "the flag beats the configured default", config: "team: sv-gruenwald\n", flag: "tc-muster", want: "tc-muster"},
		{
			name: "neither",
			err:  "No team given. Pass --team SLUG, or set a default with `kurze-url config set team SLUG`.",
		},
		{
			name: "a flag that is not a membership",
			flag: "other-team",
			err:  "You are not a member of the team “other-team”. Run `kurze-url team list` to see your teams.",
		},
		{
			name:   "a configured default that is not a membership",
			config: "team: old-team\n",
			err:    "You are not a member of the team “old-team”.",
		},
		{
			name: "a slug is matched exactly",
			flag: "TC-MUSTER",
			err:  "“TC-MUSTER”",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.signIn()
			if tt.config != "" {
				h.writeConfig(tt.config)
			}
			h.serveMe(teamGruenwald, teamMuster)
			a := newTestApp(t, h)

			got, err := a.resolveTeam(context.Background(), tt.flag)

			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got.Slug)
			require.NotEmpty(t, got.TeamID, "the membership carries the ID the API addresses the team by")
		})
	}
}

// Asking for a team without naming one needs no request: the answer is in
// the arguments and the config, and a person who forgot --team should not
// wait for the network to be told so.
func TestResolveTeamWithoutAnyTeamMakesNoRequest(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	a := newTestApp(t, h)

	_, err := a.resolveTeam(context.Background(), "")

	require.Error(t, err)
	require.Empty(t, h.api.requests())
}

func TestResolveTeamSpeaksTheConfiguredLanguage(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("language: de\n")
	h.serveMe(teamGruenwald)
	a := newTestApp(t, h)

	_, err := a.resolveTeam(context.Background(), "")

	require.ErrorContains(t, err, "Kein Team angegeben.")
}

func TestCatalogForNeverReadsTheRealConfigWithoutAnEnvironment(t *testing.T) {
	// A Deps built by hand without Getenv (the version tests do) must not
	// fall back to the real process environment or the real config file.
	d := &Deps{}

	require.Equal(t, i18n.EN, catalogFor(d).Lang())
}

func TestCatalogForIgnoresABrokenConfig(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("language: fr\n")
	h.env["LANG"] = "de_DE.UTF-8"

	require.Equal(t, i18n.DE, catalogFor(h.deps).Lang(),
		"a failure caused by the config must still be worded, from the environment")
}
