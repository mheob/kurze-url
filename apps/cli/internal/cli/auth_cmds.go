package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/auth"
	"github.com/mheob/kurze-url/apps/cli/internal/config"
	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
	"github.com/mheob/kurze-url/apps/cli/internal/output"
)

// maxTeamAttempts is how often login asks for a default team before it gives
// up and sets none. A person who typed three wrong answers is not going to
// type a fourth, and the hint says how to set one later.
const maxTeamAttempts = 3

// newLoginCmd signs the person in through the browser.
func newLoginCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:               "login",
		Short:             "Sign in to your kurze-url account",
		Args:              usageArgs(cobra.NoArgs),
		ValidArgsFunction: noValues,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.login(cmd.Context())
		},
	}
}

// newLogoutCmd ends the CLI's own session.
func newLogoutCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:               "logout",
		Short:             "Sign out of the CLI",
		Args:              usageArgs(cobra.NoArgs),
		ValidArgsFunction: noValues,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.logout(cmd.Context())
		},
	}
}

// newWhoamiCmd prints who is signed in.
func newWhoamiCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:               "whoami",
		Short:             "Show the signed-in account and the default team",
		Args:              usageArgs(cobra.NoArgs),
		ValidArgsFunction: noValues,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.whoami(cmd.Context())
		},
	}
}

// login runs the browser flow, stores the tokens, and settles the default
// team. Everything it says goes to stderr: a login has no result.
//
// The tokens are stored before anything else is asked of the API, because
// the memberships are read with them and a failure after this point must not
// cost the person a login they just completed.
func (a *App) login(ctx context.Context) error {
	endpoints, err := a.discover(ctx)
	if err != nil {
		return fmt.Errorf("find the authorization server: %w", err)
	}
	flow := auth.Flow{
		Endpoints:      endpoints,
		ClientID:       a.Env.ClientID,
		PublishableKey: a.Env.PublishableKey,
		HTTPClient:     a.d.HTTPClient,
		Ports:          a.d.LoginPorts,
		Timeout:        a.d.LoginTimeout,
		OpenBrowser:    a.d.OpenBrowser,
		Stderr:         a.d.Stderr,
		Catalog:        a.Cat,
	}
	tokens, err := flow.Login(ctx)
	if err != nil {
		return err
	}
	if err := a.Store.Save(tokens); err != nil {
		return fmt.Errorf("store the login: %w", err)
	}
	a.reportFallback()

	me, err := a.me(ctx)
	if err != nil {
		return err
	}
	team, err := a.settleDefaultTeam(me)
	if err != nil {
		return err
	}

	// The email is the server's text and goes to the terminal, so it is
	// cleaned (output.Clean says why). The team is a slug the server sent too,
	// or one the person's own config named and the server confirmed.
	email := output.Clean(me.Email)
	if team == "" {
		a.notice(a.Cat.T("login.doneNoTeam", i18n.Args{"email": email}))
		return nil
	}
	a.notice(a.Cat.T("login.done", i18n.Args{"email": email, "team": output.Clean(team)}))
	return nil
}

// reportFallback tells the person, once, that the tokens went to a file and
// not to the keychain, and where the file is. On a shared machine a
// credential must never land on disk without the person knowing.
func (a *App) reportFallback() {
	reporter, ok := a.Store.(credentials.FallbackReporter)
	if !ok || !reporter.FellBack() {
		return
	}
	a.notice(a.Cat.T("login.storedInFile", i18n.Args{"path": reporter.FilePath()}))
}

// settleDefaultTeam decides the default team after a login and saves the
// config when it changed. It returns the default's slug, or "" when there is
// none.
//
// A configured default that is still a membership is kept: logging in again
// should not ask the same question twice. Otherwise one membership is the
// default without asking, several are asked about when a person is there to
// answer, and with no one to ask (or no answer) no default is set and the
// hint says how to set one. A default the person no longer has counts as
// none, and is replaced only when something takes its place.
func (a *App) settleDefaultTeam(me apiclient.Me) (string, error) {
	current := a.configuredTeam()
	if _, ok := membershipOf(me, current); ok {
		return current, nil
	}

	var chosen string
	switch len(me.Memberships) {
	case 0:
		a.notice(a.Cat.T("login.noTeams"))
		return "", nil
	case 1:
		chosen = me.Memberships[0].Slug
	default:
		if a.d.StdinTTY() {
			chosen = a.askTeam(me.Memberships)
		}
		if chosen == "" {
			a.notice(a.Cat.T("login.noDefaultHint"))
			return "", nil
		}
	}

	if err := a.Config.Set(config.Team, "", chosen); err != nil {
		return "", err
	}
	if err := a.Config.Save(); err != nil {
		return "", fmt.Errorf("save the default team: %w", err)
	}
	return chosen, nil
}

// askTeam lists the teams by number on stderr and reads the choice from
// stdin. It returns the chosen team's slug, or "" when there is none: three
// answers that were not a team's number, or the end of the input.
//
// The end of the input is not asked again. A closed stdin never answers, and
// looping on it would print the prompt three times for nothing.
//
// A team's name is its admin's text and is listed here as it came, so it is
// cleaned before it is printed (output.Clean says why). The slug that is
// returned is not: it is what gets stored.
func (a *App) askTeam(teams []apiclient.Membership) string {
	in := bufio.NewReader(a.d.Stdin)
	last := strconv.Itoa(len(teams))

	a.notice(a.Cat.T("login.chooseTeam"))
	for i, team := range teams {
		a.notice("  " + a.Cat.T("login.teamOption", i18n.Args{
			"number": strconv.Itoa(i + 1),
			"slug":   output.Clean(team.Slug),
			"name":   output.Clean(team.Name),
		}))
	}

	for range maxTeamAttempts {
		// A prompt waits on the same line, so it is printed without a
		// newline.
		_, _ = fmt.Fprint(a.d.Stderr, a.Cat.T("login.chooseTeamPrompt", i18n.Args{"max": last}))
		line, err := in.ReadString('\n')

		// A last line without its newline is still an answer.
		if n, convErr := strconv.Atoi(strings.TrimSpace(line)); convErr == nil && n >= 1 && n <= len(teams) {
			if err != nil {
				_, _ = fmt.Fprintln(a.d.Stderr)
			}
			return teams[n-1].Slug
		}
		if err != nil {
			// Leave the prompt's line, so what follows starts on its own.
			_, _ = fmt.Fprintln(a.d.Stderr)
			return ""
		}
		a.notice(a.Cat.T("login.chooseTeamInvalid", i18n.Args{"max": last}))
	}
	return ""
}

// logout ends the CLI's own session at the server and deletes the stored
// tokens. It never signs the person out of a browser: the revocation is for
// this session alone.
//
// Deleting the tokens does not depend on the revocation. A server that cannot
// be reached, or that refuses, must not leave a credential on this machine
// that the person asked to be rid of, so the failure is a warning and the
// deletion happens anyway.
//
// The warning about that failure is printed after the deletion, because it
// says the login "was deleted anyway", and a deletion that then fails would
// make it false. When it fails, the warning keeps to what is true: the server
// still has the session.
func (a *App) logout(ctx context.Context) error {
	if _, err := a.Store.Load(); errors.Is(err, credentials.ErrNotFound) {
		return a.logoutWithoutLogin()
	}

	remoteErr := a.endSession(ctx)
	deleteErr := a.Store.Delete()

	if remoteErr != nil {
		reason := i18n.Args{"reason": describe(remoteErr, a.Cat)}
		if deleteErr == nil {
			a.notice(a.Cat.T("logout.remoteFailed", reason))
		} else {
			a.notice(a.Cat.T("logout.remoteFailedKept", reason))
		}
	}
	if deleteErr != nil {
		if !onlyKeychainUnavailable(deleteErr) {
			return a.deleteFailed(deleteErr)
		}
		a.notice(a.Cat.T("logout.keychainUnavailable"))
	}
	a.notice(a.Cat.T("logout.done"))
	return nil
}

// logoutWithoutLogin is logout when the store holds no login. That is not the
// same as "not signed in": Load cannot tell an empty store from a keychain it
// failed to ask (a locked macOS keychain over SSH), and a refresh token may
// still be there. So the store is told to delete, and "You are not signed in"
// is said only when that worked and found nothing wrong.
func (a *App) logoutWithoutLogin() error {
	err := a.Store.Delete()
	switch {
	case err == nil:
		a.notice(a.Cat.T("logout.notSignedIn"))
	case onlyKeychainUnavailable(err):
		a.notice(a.Cat.T("logout.keychainUnavailable"))
	default:
		return a.deleteFailed(err)
	}
	return nil
}

// deleteFailed is the failure for tokens that could not be deleted and may
// still be on disk. It is worded in the catalog, with the cause as the store
// reported it after it: the person needs the path and the reason to fix it.
func (a *App) deleteFailed(err error) error {
	return errors.New(a.Cat.T("logout.deleteFailed", i18n.Args{"reason": describe(err, a.Cat)}))
}

// endSession ends the session at the server and returns why it could not. A
// login the server no longer knows is not a failure to report: there is
// nothing at its end to revoke. Any other trouble, getting a usable token
// included, means the revocation did not happen.
func (a *App) endSession(ctx context.Context) error {
	token, err := a.Session.AccessToken(ctx)
	switch {
	case err == nil:
		return auth.Logout(ctx, a.d.HTTPClient, a.Env.SupabaseURL, a.Env.PublishableKey, token)
	case errors.Is(err, auth.ErrLoginRequired):
		return nil
	}
	return err
}

// onlyKeychainUnavailable reports whether every failure in err is the
// keychain being out of reach.
//
// Store.Delete joins its failures: a keychain that cannot be used and a file
// that cannot be removed arrive as one errors.Join, which matches
// credentials.ErrKeychainUnavailable as a whole. errors.Is on the whole would
// call that a warning and hide a token still on disk, so the joined parts are
// judged one by one, and the warning is for the case where the keychain is
// the whole story.
//
// Only the top level is split. A part is a keychain failure when anything in
// its chain is the sentinel, because the keychain's own error is itself a
// multi-%w wrapper of the sentinel and the operating system's cause, which
// must not be mistaken for a second, separate failure. A store that reports
// the sentinel in some other shape therefore reads as a failure and not as a
// warning: the cautious side, where the person is told.
func onlyKeychainUnavailable(err error) bool {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return errors.Is(err, credentials.ErrKeychainUnavailable)
	}
	for _, part := range joined.Unwrap() {
		if !errors.Is(part, credentials.ErrKeychainUnavailable) {
			return false
		}
	}
	return true
}

// whoami prints the signed-in email and the default team to stdout: the
// email on the first line, always, so a script can read it with `head -1`.
//
// A configured default the person is no longer a member of is not reported
// as their default. It is named in a warning instead, because the next
// command that takes the default would fail with the same words.
func (a *App) whoami(ctx context.Context) error {
	me, err := a.me(ctx)
	if err != nil {
		return err
	}
	// The email is the server's text, cleaned for the terminal like every
	// name in this package (output.Clean says why).
	if err := a.result(output.Clean(me.Email)); err != nil {
		return err
	}

	slug := a.configuredTeam()
	if _, ok := membershipOf(me, slug); ok {
		return a.result(a.Cat.T("whoami.defaultTeam", i18n.Args{"team": slug}))
	}
	if slug != "" {
		a.notice(a.Cat.T("team.notMember", i18n.Args{"slug": slug}))
	}
	return a.result(a.Cat.T("whoami.noDefaultTeam"))
}
