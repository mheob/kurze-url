package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
)

// ---- login ----

func TestLoginWithOneTeamSetsTheDefault(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald)

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Contains(t, h.stderr.String(), "Signed in as ada@example.org · default team: sv-gruenwald")
	require.Contains(t, h.config(), "team: sv-gruenwald")
	require.Empty(t, h.stdout.String(), "a login has no result, so nothing goes to stdout")
}

func TestLoginStoresTheTokensAndUsesThem(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald)

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	tokens, err := h.store.Load()
	require.NoError(t, err)
	require.NotEmpty(t, tokens.AccessToken)
	require.NotEmpty(t, tokens.RefreshToken)
	require.NotEmpty(t, h.storeEnvName, "the store is keyed by an environment name")
	require.NotEmpty(t, h.storeDir)

	// The request that read the memberships carried the token the login
	// exchanged, so it was stored before it was used.
	calls := h.api.callsTo(http.MethodGet, "/v1/me")
	require.Len(t, calls, 1)
	require.True(t, calls[0].Header.Get("Authorization") == "Bearer "+tokens.AccessToken,
		"the API did not receive the exchanged access token")

	// Discovery ran once, with the publishable key, and the browser was sent
	// to the endpoint it announced.
	discovery := h.auth.callsTo(stubDiscoveryPath)
	require.Len(t, discovery, 1)
	require.Equal(t, testAPIKey, discovery[0].Apikey)
	require.Len(t, h.opened, 1)
	require.True(t, strings.HasPrefix(h.opened[0], h.auth.URL+stubAuthorizePath+"?"), h.opened[0])
}

func TestLoginKeepsTokensOutOfTheOutput(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald)

	require.Equal(t, 0, h.run("login"), h.stderr.String())

	tokens, err := h.store.Load()
	require.NoError(t, err)
	for _, stream := range []string{h.stdout.String(), h.stderr.String()} {
		require.False(t, strings.Contains(stream, tokens.AccessToken), "the access token was printed")
		require.False(t, strings.Contains(stream, tokens.RefreshToken), "the refresh token was printed")
	}
}

func TestLoginKeepsAValidExistingDefault(t *testing.T) {
	h := newHarness(t)
	const existing = "team: tc-muster\n"
	h.writeConfig(existing)
	h.serveMe(teamGruenwald, teamMuster)
	// A terminal would be asked, if the login asked at all.
	h.stdinTTY = true

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Contains(t, h.stderr.String(), "Signed in as ada@example.org · default team: tc-muster")
	require.Equal(t, existing, h.config(), "the file is rewritten only when the default changed")
	require.NotContains(t, h.stderr.String(), "Which one should be your default?")
}

func TestLoginAsksOnATTYWithSeveralTeams(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald, teamMuster)
	h.stdinTTY = true
	h.setStdin("2\n")

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Contains(t, h.config(), "team: tc-muster")
	require.Contains(t, h.stderr.String(), "default team: tc-muster")
	// The prompt lists the teams by number, on stderr.
	require.Contains(t, h.stderr.String(), "1) sv-gruenwald (SV Grünwald)")
	require.Contains(t, h.stderr.String(), "2) tc-muster (TC Muster)")
	require.Contains(t, h.stderr.String(), "Default team (1-2): ")
	require.Empty(t, h.stdout.String())
}

func TestLoginPromptAsksAgainAfterABadAnswer(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald, teamMuster)
	h.stdinTTY = true
	h.setStdin("sv-gruenwald\n9\n1\n")

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Contains(t, h.config(), "team: sv-gruenwald")
	require.Equal(t, 2, strings.Count(h.stderr.String(), "Please enter a number from 1 to 2."))
}

func TestLoginPromptGivesUpAfterThreeAttempts(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald, teamMuster)
	h.stdinTTY = true
	// A fourth line that would be valid must not be read.
	h.setStdin("a\nb\nc\n1\n")

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Empty(t, h.config(), "no default was chosen")
	require.Equal(t, 3, strings.Count(h.stderr.String(), "Default team (1-2): "))
	require.Contains(t, h.stderr.String(), "kurze-url config set team SLUG")
	require.Contains(t, h.stderr.String(), "Signed in as ada@example.org\n")
}

func TestLoginPromptEndsAtEndOfInput(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald, teamMuster)
	h.stdinTTY = true
	h.setStdin("")

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Empty(t, h.config())
	require.Equal(t, 1, strings.Count(h.stderr.String(), "Default team (1-2): "), "EOF is not asked again")
	require.Contains(t, h.stderr.String(), "kurze-url config set team SLUG")
}

func TestLoginPromptTakesAnAnswerWithoutANewline(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald, teamMuster)
	h.stdinTTY = true
	h.setStdin("2")

	require.Equal(t, 0, h.run("login"), h.stderr.String())
	require.Contains(t, h.config(), "team: tc-muster")
}

func TestLoginWithoutATTYSetsNoDefault(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald, teamMuster)
	h.setStdin("2\n")

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Empty(t, h.config(), "no config file, so no team key")
	require.Contains(t, h.stderr.String(), "No default team is set. Set one with `kurze-url config set team SLUG`")
	require.Contains(t, h.stderr.String(), "Signed in as ada@example.org\n")
	require.NotContains(t, h.stderr.String(), "Default team (1-2)", "a pipe is never prompted")
}

func TestLoginWithoutTeamsSaysSo(t *testing.T) {
	h := newHarness(t)
	h.serveMe()

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Contains(t, h.stderr.String(), "You are not a member of any team yet.")
	require.Contains(t, h.stderr.String(), "Signed in as ada@example.org\n")
	require.Empty(t, h.config())
}

func TestLoginReplacesADefaultTheUserNoLongerHas(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("team: old-team\n")
	h.serveMe(teamGruenwald)

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Contains(t, h.config(), "team: sv-gruenwald")
	require.NotContains(t, h.config(), "old-team")
}

func TestLoginSaysOnceWhenTheTokenGoesToAFile(t *testing.T) {
	h := newHarness(t)
	const path = "/home/ada/.config/kurze-url/credentials-test.json"
	h.store.FallbackPath = path
	h.serveMe(teamGruenwald)

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, 1, strings.Count(h.stderr.String(), path), h.stderr.String())
	require.Empty(t, h.stdout.String())
}

func TestLoginSaysNothingAboutAFileWhenTheKeychainWorked(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald)

	require.Equal(t, 0, h.run("login"), h.stderr.String())

	require.NotContains(t, h.stderr.String(), "keychain")
}

func TestLoginFailsWhenTheBrowserDoesNotComeBack(t *testing.T) {
	h := newHarness(t)
	h.serveMe(teamGruenwald)
	h.deps.OpenBrowser = func(string) error { return errors.New("no browser here") }
	h.deps.LoginTimeout = 150 * time.Millisecond

	code := h.run("login")

	require.Equal(t, 1, code)
	// The login printed the URL it could not open, and the timeout is
	// worded in the person's language, not as the sentinel's English.
	require.Contains(t, h.stderr.String(), "Could not open a browser. Open this address to sign in: "+h.auth.URL)
	require.Contains(t, h.stderr.String(), "kurze-url: The sign-in did not finish in time.")
	_, err := h.store.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)
}

// ---- logout ----

func TestLogoutEndsOnlyTheCLIsSession(t *testing.T) {
	h := newHarness(t)
	h.signIn()

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	calls := h.auth.callsTo(stubLogoutPath)
	require.Len(t, calls, 1)
	require.Equal(t, "local", calls[0].Query.Get("scope"))
	require.Equal(t, "Bearer "+signedInAccess, calls[0].Authorization)
	require.Equal(t, testAPIKey, calls[0].Apikey)
	_, err := h.store.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)
	require.Equal(t, "Signed out of the CLI. Your browser sessions stay signed in.\n", h.stderr.String())
	require.Empty(t, h.stdout.String())
}

func TestLogoutDeletesTokensEvenWhenTheServerFails(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.auth.failLogout(http.StatusInternalServerError)

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	_, err := h.store.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)
	require.Contains(t, h.stderr.String(), "Could not end the session at the server")
	require.Contains(t, h.stderr.String(), "was deleted anyway")
	require.Contains(t, h.stderr.String(), "Signed out of the CLI.")
}

func TestLogoutWhenNotSignedIn(t *testing.T) {
	h := newHarness(t)

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, "You are not signed in.\n", h.stderr.String())
	require.Empty(t, h.stdout.String())
	require.Empty(t, h.auth.callsTo(stubLogoutPath), "there is no session to end")
}

func TestLogoutSkipsTheServerWhenTheLoginIsAlreadyGone(t *testing.T) {
	h := newHarness(t)
	// An expired access token whose refresh token the server no longer knows:
	// there is no session left to end, which is not a failure to report.
	require.NoError(t, h.store.Save(credentials.Tokens{
		AccessToken:  "expired-access",
		RefreshToken: "unknown-refresh",
		ExpiresAt:    testNow.Add(-time.Hour),
	}))

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	require.Empty(t, h.auth.callsTo(stubLogoutPath))
	_, err := h.store.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)
	require.NotContains(t, h.stderr.String(), "Could not end the session")
	require.Contains(t, h.stderr.String(), "Signed out of the CLI.")
}

func TestLogoutWarnsWhenTheServerCannotBeReached(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.auth.Close()

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	_, err := h.store.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)
	// The reason is worded in the person's language, not as a Go error.
	require.Contains(t, h.stderr.String(), "Could not end the session at the server (Could not reach ")
}

// deleteWatch records how a command used the store's Delete.
type deleteWatch struct {
	// calls is how often Delete ran.
	calls int
	// stderrAtDelete is what the command had printed when Delete ran, which
	// shows whether it spoke of a deletion before it had happened.
	stderrAtDelete string
}

// watchedStore is a Store that deletes for real and then reports err, as a
// Chain does when the keychain refuses, and notes the call in watch.
type watchedStore struct {
	credentials.Store
	err   error
	watch *deleteWatch
	h     *harness
}

func (s watchedStore) Delete() error {
	s.watch.calls++
	s.watch.stderrAtDelete = s.h.stderr.String()
	_ = s.Store.Delete()
	return s.err
}

// watchDelete makes the command's store report err from Delete and returns
// what it saw of the call. A nil err is a Delete that worked.
func (h *harness) watchDelete(err error) *deleteWatch {
	watch := &deleteWatch{}
	h.deps.NewStore = func(string, string) credentials.Store {
		return watchedStore{Store: h.store, err: err, watch: watch, h: h}
	}
	return watch
}

func (h *harness) failDeleteWith(err error) {
	h.watchDelete(err)
}

// keychainOnly is what Chain.Delete reports when the keychain was the only
// thing that failed.
func keychainOnly() error {
	return errors.Join(fmt.Errorf("%w: delete from the keychain: %w", credentials.ErrKeychainUnavailable, errors.New("locked")))
}

func TestLogoutWarnsWhenTheKeychainIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.failDeleteWith(errors.Join(
		fmt.Errorf("%w: delete from the keychain: %w", credentials.ErrKeychainUnavailable, errors.New("locked"))))

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	require.Contains(t, h.stderr.String(), "The system keychain could not be reached")
	require.Contains(t, h.stderr.String(), "Signed out of the CLI.")
}

func TestLogoutFailsWhenTheFileCannotBeRemovedToo(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.failDeleteWith(errors.Join(
		fmt.Errorf("%w: delete from the keychain: %w", credentials.ErrKeychainUnavailable, errors.New("locked")),
		errors.New("delete the credentials file: permission denied")))

	code := h.run("logout")

	require.Equal(t, 1, code, "a token that is still on disk is a failure, whatever the keychain says")
	require.Contains(t, h.stderr.String(), "permission denied")
	require.NotContains(t, h.stderr.String(), "Signed out of the CLI.")
}

func TestLogoutFailsOnAnyOtherDeleteError(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.failDeleteWith(errors.New("disk on fire"))

	code := h.run("logout")

	require.Equal(t, 1, code)
	require.Contains(t, h.stderr.String(), "disk on fire")
}

// Load reads a keychain it cannot open as "nothing stored" (a locked macOS
// keychain over SSH), so a refresh token can still be on the machine when it
// says so. Logout asks the store to delete either way.
func TestLogoutWhenNotSignedInStillAsksTheStoreToDelete(t *testing.T) {
	h := newHarness(t)
	watch := h.watchDelete(nil)

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, 1, watch.calls)
	require.Equal(t, "You are not signed in.\n", h.stderr.String())
}

func TestLogoutWithNoLoginFoundWarnsWhenTheKeychainIsUnavailable(t *testing.T) {
	h := newHarness(t)
	watch := h.watchDelete(keychainOnly())

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, 1, watch.calls)
	require.Equal(t, "The system keychain could not be reached, so a login stored there may still exist.\n",
		h.stderr.String())
	require.NotContains(t, h.stderr.String(), "not signed in", "nothing says there is no login")
	require.Empty(t, h.auth.callsTo(stubLogoutPath), "there is no session to end")
}

func TestLogoutWithNoLoginFoundFailsWhenTheDeleteDoes(t *testing.T) {
	h := newHarness(t)
	h.watchDelete(errors.New("delete the credentials file: permission denied"))

	code := h.run("logout")

	require.Equal(t, 1, code, "a token that is still on disk is a failure, whatever Load said")
	require.Contains(t, h.stderr.String(), "so it may still be there")
	require.Contains(t, h.stderr.String(), "permission denied")
	require.NotContains(t, h.stderr.String(), "not signed in")
}

// The warning says the stored login "was deleted anyway", so it may only be
// printed once Delete has returned success.
func TestLogoutSaysDeletedAnywayOnlyAfterTheDelete(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.auth.failLogout(http.StatusInternalServerError)
	watch := h.watchDelete(nil)

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, 1, watch.calls)
	require.Empty(t, watch.stderrAtDelete, "nothing was said before the login was gone")
	require.Contains(t, h.stderr.String(), "was deleted anyway")
	require.Contains(t, h.stderr.String(), "Signed out of the CLI.")
}

func TestLogoutDoesNotSayDeletedAnywayWhenTheDeleteFailed(t *testing.T) {
	t.Run("the keychain alone", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		h.auth.failLogout(http.StatusInternalServerError)
		h.watchDelete(keychainOnly())

		code := h.run("logout")

		require.Equal(t, 0, code, h.stderr.String())
		require.NotContains(t, h.stderr.String(), "deleted anyway")
		// Both facts reach the person: the server still has the session, and
		// the keychain may still have the login.
		require.Contains(t, h.stderr.String(), "Could not end the session at the server")
		require.Contains(t, h.stderr.String(), "The system keychain could not be reached")
	})

	t.Run("a token left on disk", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		h.auth.failLogout(http.StatusInternalServerError)
		h.watchDelete(errors.New("delete the credentials file: permission denied"))

		code := h.run("logout")

		require.Equal(t, 1, code)
		require.NotContains(t, h.stderr.String(), "deleted anyway")
		require.NotContains(t, h.stderr.String(), "Signed out of the CLI.")
		require.Contains(t, h.stderr.String(), "Could not end the session at the server")
		require.Contains(t, h.stderr.String(), "so it may still be there")
		require.Contains(t, h.stderr.String(), "permission denied")
	})
}

func TestLogoutWordsAFailedDeleteInGerman(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("language: de\n")
	h.watchDelete(errors.New("delete the credentials file: permission denied"))

	code := h.run("logout")

	require.Equal(t, 1, code)
	require.Contains(t, h.stderr.String(), "konnte nicht gelöscht werden")
	require.Contains(t, h.stderr.String(), "permission denied")
}

func TestLogoutRefusesAnArgument(t *testing.T) {
	h := newHarness(t)

	require.Equal(t, 2, h.run("logout", "everywhere"))
}

// ---- whoami ----

func TestWhoamiPrintsEmailAndDefaultTeam(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("team: sv-gruenwald\n")
	h.serveMe(teamGruenwald, teamMuster)

	code := h.run("whoami")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, "ada@example.org\nDefault team: sv-gruenwald\n", h.stdout.String())
	require.Empty(t, h.stderr.String())
}

func TestWhoamiWithoutADefaultTeam(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.serveMe(teamGruenwald, teamMuster)

	code := h.run("whoami")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, "ada@example.org\nNo default team\n", h.stdout.String())
}

func TestWhoamiDoesNotClaimADefaultTheUserNoLongerHas(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("team: old-team\n")
	h.serveMe(teamGruenwald)

	code := h.run("whoami")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, "ada@example.org\nNo default team\n", h.stdout.String())
	require.Contains(t, h.stderr.String(), "You are not a member of the team “old-team”.")
}

func TestWhoamiRefreshesAnExpiredToken(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	require.NoError(t, h.store.Save(credentials.Tokens{
		AccessToken:  "expired-access",
		RefreshToken: signedInRefresh,
		ExpiresAt:    testNow.Add(-time.Minute),
	}))
	h.serveMe(teamGruenwald)

	code := h.run("whoami")

	require.Equal(t, 0, code, h.stderr.String())
	require.Len(t, h.auth.callsTo(stubTokenPath), 1)
	// The refreshed pair was written back before anything used it.
	tokens, err := h.store.Load()
	require.NoError(t, err)
	require.NotEqual(t, signedInRefresh, tokens.RefreshToken)
}

func TestWhoamiWithoutALoginAsksToLogIn(t *testing.T) {
	h := newHarness(t)

	code := h.run("whoami")

	require.Equal(t, 1, code)
	require.Equal(t, "kurze-url: Please run `kurze-url login` again.\n", h.stderr.String())
	require.Empty(t, h.stdout.String())
	require.Empty(t, h.api.requests(), "no request is made without a token")
}

func TestCommandsInGermanWhenConfigured(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("language: de\nteam: sv-gruenwald\n")
	h.serveMe(teamGruenwald)

	code := h.run("whoami")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, "ada@example.org\nStandard-Team: sv-gruenwald\n", h.stdout.String())
}

func TestLogoutInGerman(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("language: de\n")

	code := h.run("logout")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t,
		"Aus der CLI abgemeldet. Deine Browser-Sitzungen bleiben angemeldet.\n",
		h.stderr.String())
}

func TestLoginInGerman(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("language: de\n")
	h.serveMe(teamGruenwald)

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	require.Contains(t, h.stderr.String(), "Warte auf die Anmeldung im Browser.")
	require.Contains(t, h.stderr.String(), "Angemeldet als ada@example.org · Standard-Team: sv-gruenwald")
}

func TestLanguageFollowsTheEnvironmentWithoutAConfigValue(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.env["LANG"] = "de_DE.UTF-8"
	h.serveMe(teamGruenwald)

	require.Equal(t, 0, h.run("whoami"), h.stderr.String())

	require.Equal(t, "ada@example.org\nKein Standard-Team\n", h.stdout.String())
}

func TestLoginRefusesAnArgument(t *testing.T) {
	h := newHarness(t)

	require.Equal(t, 2, h.run("login", "now"))
	require.Empty(t, h.auth.callsTo(stubDiscoveryPath))
}

func TestWhoamiRefusesAnArgument(t *testing.T) {
	h := newHarness(t)

	require.Equal(t, 2, h.run("whoami", "me"))
}

func TestOnlyKeychainUnavailable(t *testing.T) {
	keychain := fmt.Errorf("%w: delete from the keychain: %w", credentials.ErrKeychainUnavailable, errors.New("locked"))
	file := errors.New("delete the credentials file: permission denied")

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"the keychain alone, as Chain.Delete joins it", errors.Join(keychain), true},
		{"the keychain and the file", errors.Join(keychain, file), false},
		{"the file alone", errors.Join(file), false},
		{"the sentinel wrapped once", fmt.Errorf("delete: %w", credentials.ErrKeychainUnavailable), true},
		{"something else", errors.New("disk on fire"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, onlyKeychainUnavailable(tt.err))
		})
	}
}
