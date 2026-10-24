package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/auth"
	"github.com/mheob/kurze-url/apps/cli/internal/config"
	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
	"github.com/mheob/kurze-url/apps/cli/internal/environment"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

// App is what a command that talks to the service works with: the instance it
// talks to, the config file, the language, the credentials and a client that
// signs its requests with them.
//
// It is built lazily by newApp, in the RunE of each command that needs it, and
// never for `version` or `completion`: those must work with a broken config
// file or a bad environment override, which is exactly when a person reaches
// for `version` to paste into a bug report.
//
// An App serves one command in one process. It caches what it has fetched and
// is not safe for concurrent use, and it must not be copied.
type App struct {
	// Env is the instance: API and Supabase URLs, the OAuth client, the
	// shared short-link host.
	Env environment.Environment
	// Config is the loaded config file. Commands that change it Save it.
	Config *config.File
	// Cat words everything the command prints, in the language the config
	// or the environment asked for.
	Cat *i18n.Catalog
	// Store holds the tokens.
	Store credentials.Store
	// Session hands out the current access token and refreshes it.
	Session *auth.Session
	// API is the typed client, signing its requests through Session.
	API *apiclient.Client

	// d is where the App prints and reads: Deps' streams, clock and HTTP
	// client.
	d *Deps

	// discovery runs once per process: the endpoints do not change between
	// the login and the refresh a few seconds later.
	discoverOnce sync.Once
	endpoints    auth.Endpoints
	discoverErr  error

	// me is /v1/me as first fetched.
	meLoaded bool
	meCached apiclient.Me
}

// newApp reads the config file and the environment, picks the language and
// wires the credential store, the session and the API client together.
//
// It fails with the config package's *ValidationError for a config file that
// breaks a rule, and with the environment package's *InvalidURLError for a
// bad override. Both are typed so describe can word them, and neither is
// wrapped in text of this package's, which would only be English.
func newApp(d *Deps) (*App, error) {
	cfg, err := loadConfig(d)
	if err != nil {
		return nil, err
	}
	env, err := environment.FromEnv(d.Getenv)
	if err != nil {
		return nil, err
	}

	configured, _ := cfg.Get(config.Language, "")
	cat := i18n.Load(i18n.Detect(configured, d.Getenv, i18n.SystemLanguage))
	store := d.NewStore(env.Name, d.CredentialsDir)

	a := &App{Env: env, Config: cfg, Cat: cat, Store: store, d: d}
	a.Session = &auth.Session{
		Store:          store,
		Endpoints:      a.discover,
		ClientID:       env.ClientID,
		PublishableKey: env.PublishableKey,
		HTTPClient:     d.HTTPClient,
		Now:            d.Now,
	}
	a.API = apiclient.New(apiclient.Options{
		BaseURL:      env.APIURL,
		HTTPClient:   d.HTTPClient,
		Tokens:       a.Session,
		VercelBypass: env.VercelBypass,
		UserAgent:    programName + "/" + d.Build.Version,
	})
	return a, nil
}

// loadConfig reads the config file where KURZE_URL_CONFIG or the user's
// config directory says it is. A file that does not exist is an empty one.
func loadConfig(d *Deps) (*config.File, error) {
	path, err := config.Path(d.Getenv)
	if err != nil {
		return nil, fmt.Errorf("find the config file: %w", err)
	}
	return config.Load(path)
}

// catalogFor picks the language a failure is worded in when no App exists to
// say: Run reports an error after the command has returned, and a command
// that failed in newApp has no catalog at all.
//
// It repeats what newApp does, the config's language first and then the
// environment, but forgives a config file that cannot be read. That is the
// very failure it may be asked to word, and the environment's language is
// the best guess left. A Deps with no Getenv (a test of `version`) speaks
// English and reads nothing, so a test can never reach the real config
// directory through it.
func catalogFor(d *Deps) *i18n.Catalog {
	if d.Getenv == nil {
		return i18n.Load(i18n.EN)
	}
	configured := ""
	if cfg, err := loadConfig(d); err == nil {
		configured, _ = cfg.Get(config.Language, "")
	}
	return i18n.Load(i18n.Detect(configured, d.Getenv, i18n.SystemLanguage))
}

// discover finds the authorization server's endpoints, at most once. It is
// the session's Endpoints and what login reads, so a command that logs in
// and then refreshes asks Supabase once.
func (a *App) discover(ctx context.Context) (auth.Endpoints, error) {
	a.discoverOnce.Do(func() {
		a.endpoints, a.discoverErr = auth.Discover(ctx, a.d.HTTPClient, a.Env.SupabaseURL, a.Env.PublishableKey)
	})
	return a.endpoints, a.discoverErr
}

// notice prints a message the person should see to stderr: a warning, a
// prompt or a status line. Results never go through it. The text is already
// in the person's language, which is why this takes text and not a key: the
// catalog check needs every key at its call site as a literal.
func (a *App) notice(text string) {
	// A failed write to the person's own terminal has nowhere to be
	// reported.
	_, _ = fmt.Fprintln(a.d.Stderr, text)
}

// result prints a line of a command's result to stdout.
func (a *App) result(text string) error {
	return writeLine(a.d.Stdout, text)
}

// writeLine writes one line and reports a failed write.
func writeLine(w io.Writer, text string) error {
	if _, err := fmt.Fprintln(w, text); err != nil {
		return fmt.Errorf("write the result: %w", err)
	}
	return nil
}

// me returns the signed-in user and their memberships, asking the API the
// first time and remembering the answer after, so a command that needs the
// memberships twice costs one request.
//
// On that first successful answer it also warns, once, about every
// `teams.<slug>` section of the config whose team the person is not a member
// of. The section is ignored, never an error: people leave teams, and a file
// they maintain by hand should not stop working when they do. Both the
// warning and the cache are here because the memberships are what the check
// needs and this is where they first arrive.
//
// A failed request is not remembered.
func (a *App) me(ctx context.Context) (apiclient.Me, error) {
	if a.meLoaded {
		return a.meCached, nil
	}
	me, err := a.API.Me(ctx)
	if err != nil {
		return apiclient.Me{}, err
	}
	a.meCached, a.meLoaded = me, true

	for _, slug := range a.Config.TeamSections() {
		if _, ok := membershipOf(me, slug); !ok {
			a.notice(a.Cat.T("config.staleTeam", i18n.Args{"slug": slug}))
		}
	}
	return me, nil
}

// membershipOf finds the membership for a team slug. The match is exact:
// slugs are lower-case by rule, and the caller folds what a person typed.
func membershipOf(me apiclient.Me, slug string) (apiclient.Membership, bool) {
	for _, m := range me.Memberships {
		if m.Slug == slug {
			return m, true
		}
	}
	return apiclient.Membership{}, false
}

// configuredTeam is the default team from the config, or "".
func (a *App) configuredTeam() string {
	slug, _ := a.Config.Get(config.Team, "")
	return slug
}

// resolveTeam turns what the person named into the membership the API
// addresses: the --team flag when given, else the config's default team.
//
// The slug is matched ignoring case. Team slugs are lower-case by rule, so a
// team typed as SV-Gruenwald can only mean sv-gruenwald, and refusing it as
// "not a member" would send the person looking for a membership problem that
// is not there. The config's slug is folded the same way, although a config
// that loaded holds a lower-case one already. A refusal names the team as it
// was typed.
//
// It fails with a message in the person's language when there is neither,
// without a request, or when the team is not one of their memberships. The
// frontend does the same slug-to-ID resolution out of GET /v1/me, for the
// same reason: it costs no extra request and keeps a second resolution path
// out of every tenancy-critical call.
func (a *App) resolveTeam(ctx context.Context, flag string) (apiclient.Membership, error) {
	typed := flag
	if typed == "" {
		typed = a.configuredTeam()
	}
	if typed == "" {
		return apiclient.Membership{}, errors.New(a.Cat.T("team.noneGiven"))
	}

	me, err := a.me(ctx)
	if err != nil {
		return apiclient.Membership{}, err
	}
	team, ok := membershipOf(me, strings.ToLower(typed))
	if !ok {
		return apiclient.Membership{}, errors.New(a.Cat.T("team.notMember", i18n.Args{"slug": typed}))
	}
	return team, nil
}
