package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/config"
	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
)

// complete asks the real command tree what Tab would offer for the words
// typed so far, through the same hidden `__complete` command the shell scripts
// call. The last word is the one being completed, and "" asks for everything
// that may come next.
//
// The answer is cobra's protocol: one candidate per line, with a description
// after a tab when there is one, then a line ":<directive>". The directive is
// returned as the number cobra prints, so a test compares it against
// int(cobra.ShellCompDirective...). Stderr is left alone, because cobra
// writes its own "Completion ended with directive" note there.
func (h *harness) complete(args ...string) (values []string, directive int) {
	h.t.Helper()
	h.stdout.Reset()

	code := h.run(append([]string{"__complete"}, args...)...)
	require.Equal(h.t, 0, code, "__complete %q failed: %s", args, h.stderr.String())

	lines := strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	require.True(h.t, strings.HasPrefix(last, ":"), "no directive line in %q", h.stdout.String())
	directive, err := strconv.Atoi(strings.TrimPrefix(last, ":"))
	require.NoError(h.t, err)
	return lines[:len(lines)-1], directive
}

func TestConfigKeysComplete(t *testing.T) {
	h := newHarness(t)
	values, directive := h.complete("config", "set", "")
	require.Equal(t, []string{"language", "team", "folder", "domain", "qr.format", "qr.size", "qr.fg", "qr.bg"}, values)
	require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive)
	for _, sub := range []string{"get", "unset"} {
		got, _ := h.complete("config", sub, "q")
		require.Equal(t, []string{"qr.format", "qr.size", "qr.fg", "qr.bg"}, got, sub)
	}
}

func TestConfigSetValuesComplete(t *testing.T) {
	h := newHarness(t)
	got, _ := h.complete("config", "set", "language", "")
	require.Equal(t, []string{"en", "de"}, got)
	got, _ = h.complete("config", "set", "qr.format", "")
	require.Equal(t, []string{"svg", "png"}, got)
	for _, key := range []string{"qr.size", "qr.fg", "qr.bg"} {
		got, directive := h.complete("config", "set", key, "")
		require.Empty(t, got, key)
		require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive, key)
	}
}

func TestQRFormatCompletes(t *testing.T) {
	h := newHarness(t)
	got, _ := h.complete("link", "qr", "sommerfest", "--format", "")
	require.Equal(t, []string{"svg", "png"}, got)
}

func TestFreeTextOffersNoFiles(t *testing.T) {
	cases := [][]string{
		{"link", "create", ""},
		{"link", "create", "https://example.org", "--slug", ""},
		{"link", "list", "--search", ""},
		{"link", "list", "--limit", ""},
		{"link", "list", ""},
		{"link", "qr", "x", "--size", ""},
		{"link", "qr", "x", "--fg", ""},
		{"link", "qr", "x", "--bg", ""},
		{"login", ""}, {"logout", ""}, {"whoami", ""}, {"team", "list", ""}, {"version", ""},
	}
	for _, args := range cases {
		h := newHarness(t)
		got, directive := h.complete(args...)
		require.Empty(t, got, args)
		require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive, args)
	}
}

func TestQROutputStillCompletesFiles(t *testing.T) {
	h := newHarness(t)
	_, directive := h.complete("link", "qr", "x", "--output", "")
	require.Equal(t, int(cobra.ShellCompDirectiveDefault), directive)
}

func TestCompletionWritesNothingToStderr(t *testing.T) {
	h := newHarness(t)
	h.complete("config", "set", "")
	require.NotContains(t, h.stderr.String(), "kurze-url:")
}

// A command takes a fixed number of words, and a word past the last one has no
// candidates: offering the keys again after `config get language` would
// suggest a command that then fails its argument count.
func TestConfigOffersNothingBeyondItsArguments(t *testing.T) {
	cases := [][]string{
		{"config", "get", "language", ""},
		{"config", "unset", "language", ""},
		{"config", "set", "language", "en", ""},
		// A key the file does not know has no values to offer.
		{"config", "set", "nope", ""},
	}
	for _, args := range cases {
		h := newHarness(t)
		got, directive := h.complete(args...)
		require.Empty(t, got, args)
		require.Equal(t, int(cobra.ShellCompDirectiveNoFileComp), directive, args)
	}
}

// Fixed values are answered from the binary: no config file is read, so a
// broken one does not silence them, and nothing touches the API.
func TestFixedValuesNeedNoNetworkAndNoConfig(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("language: fr\n")

	got, _ := h.complete("config", "set", "qr.format", "")

	require.Equal(t, []string{"svg", "png"}, got)
	require.Empty(t, h.api.requests())
	require.Empty(t, h.auth.callsTo(stubTokenPath))
}

func TestWithPrefix(t *testing.T) {
	values := []cobra.Completion{"sv-gruenwald\tSV Grünwald", "tc-muster\tTC Muster", "Sommerfest"}

	require.Equal(t, values, withPrefix(values, ""), "nothing typed keeps everything")
	require.Equal(t, []cobra.Completion{"tc-muster\tTC Muster"}, withPrefix(values, "tc"))
	require.Equal(t, []cobra.Completion{"tc-muster\tTC Muster"}, withPrefix(values, "TC"),
		"typed capitals still match, as slugs are lower-case")
	require.Equal(t, []cobra.Completion{"Sommerfest"}, withPrefix(values, "som"),
		"the comparison folds the value too")
	require.Empty(t, withPrefix(values, "muster"), "a prefix, not a substring")
	require.Empty(t, withPrefix(values, "Grünwald"), "the description is never matched")
	require.Empty(t, withPrefix(values, "sv-gruenwald\t"), "the tab is not part of the value")
}

func TestDefaultCompletionTimeoutIsTwoSeconds(t *testing.T) {
	require.Equal(t, 2*time.Second, Default(testBuild).CompletionTimeout)
	require.Equal(t, 2*time.Second, newHarness(t).deps.CompletionTimeout,
		"the harness keeps the production budget unless a test changes it")
}

// ---- completeWith ----

// neverCalled is a list function for a case in which completion must give up
// before it would ask for anything.
func neverCalled(t *testing.T) func(context.Context, *App, *cobra.Command, []string, string) ([]cobra.Completion, error) {
	return func(context.Context, *App, *cobra.Command, []string, string) ([]cobra.Completion, error) {
		t.Error("the list function must not run")
		return nil, nil
	}
}

func TestCompleteWithHandsOverTheAnswer(t *testing.T) {
	h := newHarness(t)
	// The list function runs on a goroutine of its own, where a failed
	// require would end the goroutine and not the test, so it only records
	// and the assertions are made once the answer is back.
	var gotApp *App
	var gotArgs []string
	var gotTyped string
	fn := completeWith(h.deps, func(_ context.Context, a *App, _ *cobra.Command, args []string, typed string) ([]cobra.Completion, error) {
		gotApp, gotArgs, gotTyped = a, args, typed
		return []cobra.Completion{"sv-gruenwald\tSV Grünwald"}, nil
	})

	values, directive := fn(&cobra.Command{}, []string{"one"}, "sv")

	require.Equal(t, []cobra.Completion{"sv-gruenwald\tSV Grünwald"}, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.NotNil(t, gotApp)
	require.Equal(t, []string{"one"}, gotArgs)
	require.Equal(t, "sv", gotTyped)
}

// An empty answer must still say "no files", or zsh falls back to file names
// where a team or a slug was expected.
func TestCompleteWithAnEmptyAnswerOffersNoFiles(t *testing.T) {
	h := newHarness(t)
	fn := completeWith(h.deps, func(context.Context, *App, *cobra.Command, []string, string) ([]cobra.Completion, error) {
		return nil, nil
	})

	values, directive := fn(&cobra.Command{}, nil, "")

	require.Empty(t, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompleteWithAnErrorOffersNothing(t *testing.T) {
	h := newHarness(t)
	fn := completeWith(h.deps, func(context.Context, *App, *cobra.Command, []string, string) ([]cobra.Completion, error) {
		return []cobra.Completion{"half-an-answer"}, errors.New("the API said no")
	})

	values, directive := fn(&cobra.Command{}, nil, "")

	require.Nil(t, values, "an answer that came with an error is not offered")
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Empty(t, h.stdout.String())
	require.Empty(t, h.stderr.String(), "completion never prints a failure")
}

func TestCompleteWithAPanicOffersNothing(t *testing.T) {
	h := newHarness(t)
	fn := completeWith(h.deps, func(context.Context, *App, *cobra.Command, []string, string) ([]cobra.Completion, error) {
		panic("a nil map nobody expected")
	})

	values, directive := fn(&cobra.Command{}, nil, "")

	require.Nil(t, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Empty(t, h.stderr.String(), "a panic in a Tab press must not reach the terminal")
}

// A config file that does not load is not worth a word at Tab: the command the
// person is typing reports it when it runs.
func TestCompleteWithABrokenConfigOffersNothing(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("language: fr\n")
	fn := completeWith(h.deps, neverCalled(t))

	values, directive := fn(&cobra.Command{}, nil, "")

	require.Nil(t, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Empty(t, h.stdout.String())
	require.Empty(t, h.stderr.String())
}

// Keychain reads do not honour a context, so a list function that is stuck
// must not hold the shell: the answer comes at the deadline whatever is still
// running.
func TestCompleteWithAnswersEmptyWhenTheDeadlinePasses(t *testing.T) {
	h := newHarness(t)
	h.deps.CompletionTimeout = 50 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fn := completeWith(h.deps, func(context.Context, *App, *cobra.Command, []string, string) ([]cobra.Completion, error) {
		close(started)
		<-release // ignores its context, like a locked keychain
		return []cobra.Completion{"too-late"}, nil
	})

	start := time.Now()
	values, directive := fn(&cobra.Command{}, nil, "")

	require.Nil(t, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Less(t, time.Since(start), 5*time.Second, "completion waited for work that ignores its deadline")
	// An App that cannot be built gives the same empty answer at once, and
	// the test would then pass without a deadline in it. The list function
	// running proves the App was built and the wait was the deadline's. It
	// may start after the answer under -race on a busy machine, which is
	// why this waits for it instead of asking whether it has started.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the list function never ran, so it was not the deadline that ended the wait")
	}
}

func TestCompleteWithGivesTheListFunctionTheDeadline(t *testing.T) {
	h := newHarness(t)
	h.deps.CompletionTimeout = 3 * time.Second
	var hasDeadline bool
	var remaining time.Duration
	fn := completeWith(h.deps, func(ctx context.Context, _ *App, _ *cobra.Command, _ []string, _ string) ([]cobra.Completion, error) {
		var deadline time.Time
		deadline, hasDeadline = ctx.Deadline()
		remaining = time.Until(deadline)
		return nil, nil
	})

	fn(&cobra.Command{}, nil, "")

	require.True(t, hasDeadline, "the request context carries the completion deadline")
	require.Positive(t, remaining)
	require.LessOrEqual(t, remaining, 3*time.Second)
}

// What the person sees while pressing Tab is the shell's own candidate list,
// so nothing a command normally says may reach the terminal, and nothing may
// ask or open anything. The copy is muted and the original is left alone.
func TestCompleteWithMutesTheDepsItBuildsTheAppFrom(t *testing.T) {
	h := newHarness(t)
	h.stdinTTY = true
	h.setStdin("y\n")
	var (
		resultErr, stdinErr, browserErr error
		stdinTTY                        bool
		stdinRest                       []byte
		stdout, stderr                  io.Writer
	)
	fn := completeWith(h.deps, func(_ context.Context, a *App, _ *cobra.Command, _ []string, _ string) ([]cobra.Completion, error) {
		a.notice("a warning")
		resultErr = a.result("a result")
		stdout, stderr = a.d.Stdout, a.d.Stderr
		stdinTTY = a.d.StdinTTY()
		stdinRest, stdinErr = io.ReadAll(a.d.Stdin)
		browserErr = a.d.OpenBrowser("https://example.org")
		return nil, nil
	})

	fn(&cobra.Command{}, nil, "")

	require.NoError(t, resultErr)
	require.Equal(t, io.Discard, stdout)
	require.Equal(t, io.Discard, stderr)
	require.False(t, stdinTTY, "nobody can answer a prompt during Tab")
	require.NoError(t, stdinErr)
	require.Empty(t, stdinRest, "stdin is empty, so a prompt that slipped through reads nothing")
	require.Error(t, browserErr, "completion never opens a browser")
	require.Empty(t, h.stdout.String())
	require.Empty(t, h.stderr.String())
	require.Empty(t, h.opened)
	require.Same(t, h.stdout, h.deps.Stdout, "the caller's Deps keeps its own streams")
	require.Same(t, h.stderr, h.deps.Stderr)
	require.True(t, h.deps.StdinTTY(), "the caller's Deps keeps its own terminal")
}

// ---- fixed values and their validation ----

// The completed values are lists of their own, next to the config package's
// rules and the flag check that judge what a person types, and nothing but this
// test ties them together: a value added to a completion and not to its rule
// would offer a word the command then refuses. Every value a fixed completion
// offers must therefore pass the rule that guards the same input.
func TestFixedValuesAreAcceptedByTheirValidation(t *testing.T) {
	scratch := func() *config.File {
		file, err := config.Load(filepath.Join(t.TempDir(), "config.yaml"))
		require.NoError(t, err)
		return file
	}

	h := newHarness(t)
	cases := []struct {
		name  string
		words []string
		key   config.Key
		// flagFormat also judges each value as `link qr --format`.
		flagFormat bool
	}{
		{"config set language", []string{"config", "set", "language", ""}, config.Language, false},
		{"config set qr.format", []string{"config", "set", "qr.format", ""}, config.QRFormat, true},
		{"link qr --format", []string{"link", "qr", "x", "--format", ""}, config.QRFormat, true},
	}
	for _, c := range cases {
		offered, _ := h.complete(c.words...)
		require.NotEmpty(t, offered, c.name)
		for _, value := range offered {
			require.NoError(t, scratch().Set(c.key, "", value), "%s offers %q, which the config rules refuse", c.name, value)
			if c.flagFormat {
				require.NoError(t, checkQRFlags(h.deps, qrOptions{format: value}),
					"%s offers %q, which --format refuses", c.name, value)
			}
		}
	}

	// The two format completions are one list, however it is written twice.
	viaConfig, _ := h.complete("config", "set", "qr.format", "")
	viaFlag, _ := h.complete("link", "qr", "x", "--format", "")
	require.Equal(t, viaConfig, viaFlag)
}

// ---- dynamic values ----

var (
	folderSommerfest = apiclient.Folder{ID: "00000000-0000-4000-8000-0000000000e1", Name: "Sommerfest 2026"}
	folderPresse     = apiclient.Folder{ID: "00000000-0000-4000-8000-0000000000e2", Name: "Presse"}
	folderVorstand   = apiclient.Folder{ID: "00000000-0000-4000-8000-0000000000e3", Name: "Vorstand"}

	domainVerified = apiclient.Domain{ID: "00000000-0000-4000-8000-0000000000d1", Hostname: "links.sv.de", VerificationStatus: "verified"}
	domainPending  = apiclient.Domain{ID: "00000000-0000-4000-8000-0000000000d2", Hostname: "neu.sv.de", VerificationStatus: "pending"}
)

// noFiles is the directive every answer here carries, an empty one included.
var noFiles = int(cobra.ShellCompDirectiveNoFileComp)

// requireOnlyMe fails when completion asked the API for anything but who the
// person is.
func requireOnlyMe(t *testing.T, h *harness) {
	t.Helper()
	for _, call := range h.api.requests() {
		require.Equal(t, apiclient.PathMe, call.Path, "completion asked for more than it needed")
	}
}

func TestTeamsComplete(t *testing.T) {
	h := linkHarness(t, "", teamGruenwald, teamMuster)

	got, directive := h.complete("link", "list", "--team", "")
	require.Equal(t, []string{"sv-gruenwald\tSV Grünwald", "tc-muster\tTC Muster"}, got)
	require.Equal(t, noFiles, directive)

	got, directive = h.complete("link", "list", "--team", "tc")
	require.Equal(t, []string{"tc-muster\tTC Muster"}, got)
	require.Equal(t, noFiles, directive)

	requireOnlyMe(t, h)
}

// Every command that takes --team completes it the same way, and none needs a
// default team to do so.
func TestEveryTeamFlagCompletesTeams(t *testing.T) {
	commands := [][]string{
		{"config", "get"},
		{"config", "set"},
		{"config", "unset"},
		{"link", "create"},
		{"link", "list"},
		{"link", "qr", "sommerfest"},
		{"link", "stats", "sommerfest"},
	}
	for _, command := range commands {
		h := linkHarness(t, "", teamGruenwald, teamMuster)
		got, directive := h.complete(append(command, "--team", "")...)
		require.Equal(t, []string{"sv-gruenwald\tSV Grünwald", "tc-muster\tTC Muster"}, got, command)
		require.Equal(t, noFiles, directive, command)
	}
}

func TestFoldersCompleteForTheDefaultTeam(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald, teamMuster)
	h.serveFolders(teamGruenwald, folderSommerfest, folderPresse)

	// A name with a space is one candidate: the shell quotes it, so it must
	// not arrive split.
	got, directive := h.complete("link", "list", "--folder", "")
	require.Equal(t, []string{"Sommerfest 2026", "Presse"}, got)
	require.Equal(t, noFiles, directive)

	got, _ = h.complete("link", "create", "https://example.org", "--folder", "pre")
	require.Equal(t, []string{"Presse"}, got, "typed text narrows the list, whatever its case")
	got, _ = h.complete("link", "create", "https://example.org", "--folder", "SOMMER")
	require.Equal(t, []string{"Sommerfest 2026"}, got)

	require.Empty(t, h.api.callsTo(http.MethodGet, teamPath(teamMuster, "folders")))
}

func TestFoldersFollowTheTeamOnTheLine(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald, teamMuster)
	h.serveFolders(teamGruenwald, folderSommerfest, folderPresse)
	h.serveFolders(teamMuster, folderVorstand)

	got, directive := h.complete("link", "list", "--team", "tc-muster", "--folder", "")
	require.Equal(t, []string{"Vorstand"}, got)
	require.Equal(t, noFiles, directive)

	got, _ = h.complete("link", "list", "--team", "TC-Muster", "--folder", "")
	require.Equal(t, []string{"Vorstand"}, got, "the slug on the line is folded, as resolveTeam folds it")

	require.Empty(t, h.api.callsTo(http.MethodGet, teamPath(teamGruenwald, "folders")),
		"the default team's folders were not what was asked for")
}

func TestDomainsCompleteVerifiedAndShared(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveDomains(teamGruenwald, domainVerified, domainPending)

	// The same two commands that take --domain: one creates a link, the other
	// looks one up.
	for _, words := range [][]string{
		{"link", "create", "https://example.org", "--domain", ""},
		{"link", "qr", "sommerfest", "--domain", ""},
		{"link", "stats", "sommerfest", "--domain", ""},
	} {
		got, directive := h.complete(words...)
		require.Equal(t, []string{"links.sv.de", "go.kurze-url.app"}, got, words)
		require.Equal(t, noFiles, directive, words)
	}

	got, _ := h.complete("link", "create", "https://example.org", "--domain", "li")
	require.Equal(t, []string{"links.sv.de"}, got)
}

// The shared host is the instance's, and a staging instance has another one.
func TestDomainsOfferTheInstancesOwnSharedHost(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.env["KURZE_URL_SHARED_HOST"] = "short.invalid"
	h.serveDomains(teamGruenwald, domainVerified)

	got, _ := h.complete("link", "create", "https://example.org", "--domain", "")

	require.Equal(t, []string{"links.sv.de", "short.invalid"}, got)
}

func TestLinkSlugsComplete(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald,
		listedLink("sommerfest", "https://example.org/a", "active"),
		listedLink("sommer-2025", "https://example.org/b", "active"),
		listedLink("newsletter-sommer", "https://example.org/c", "active"),
		// The API's q also matches the destination, so a slug that only
		// points at a page with the word in it comes back as well.
		listedLink("plakat", "https://example.org/sommerfest", "active"),
	)

	got, directive := h.complete("link", "qr", "somm")

	require.Equal(t, []string{
		"sommerfest\thttps://example.org/a",
		"sommer-2025\thttps://example.org/b",
	}, got)
	require.Equal(t, noFiles, directive)
	require.Equal(t, url.Values{"q": {"somm"}, "per_page": {"100"}}, h.listQuery(teamGruenwald))
}

// Nothing typed asks for no filter at all, and the slugs of the first page are
// offered.
func TestLinkSlugsCompleteBeforeAnythingIsTyped(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald, listedLink("sommerfest", "https://example.org/a", "active"))

	got, _ := h.complete("link", "stats", "")

	require.Equal(t, []string{"sommerfest\thttps://example.org/a"}, got)
	require.Equal(t, url.Values{"per_page": {"100"}}, h.listQuery(teamGruenwald))
}

func TestLinkSlugsIgnoreCase(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald,
		listedLink("sommerfest", "https://example.org/a", "active"),
		listedLink("sommer-2025", "https://example.org/b", "active"),
		listedLink("newsletter-sommer", "https://example.org/c", "active"),
	)

	got, directive := h.complete("link", "stats", "SOMM")

	require.Equal(t, []string{
		"sommerfest\thttps://example.org/a",
		"sommer-2025\thttps://example.org/b",
	}, got)
	require.Equal(t, noFiles, directive)
}

// The same slug on two domains is one word to complete: which of them is meant
// is for --domain to say.
func TestLinkSlugsAreOfferedOnce(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	onShared := listedLink("sommerfest", "https://example.org/a", "active")
	onOwn := listedLink("sommerfest", "https://example.org/b", "active")
	onOwn.Hostname = "links.sv.de"
	h.serveLinks(teamGruenwald, onShared, onOwn, listedLink("sommer-2025", "https://example.org/c", "active"))

	got, _ := h.complete("link", "qr", "somm")

	require.Equal(t, []string{
		"sommerfest\thttps://example.org/a",
		"sommer-2025\thttps://example.org/c",
	}, got)
}

// A command takes one link, so a word after it has no slug to offer.
func TestLinkSlugsStopAfterTheFirstArgument(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)

	got, directive := h.complete("link", "qr", "sommerfest", "")

	require.Empty(t, got)
	require.Equal(t, noFiles, directive)
	require.Empty(t, h.api.requests())
}

// A word with a slash or a colon is a short URL being typed. Short URLs are not
// completed, and completing one's host as a slug would only mislead.
func TestShortURLsAreNotCompleted(t *testing.T) {
	for _, typed := range []string{"go.kurze-url.app/so", "https:", "https://go.kurze-url.app/", "/so"} {
		h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)

		got, directive := h.complete("link", "qr", typed)

		require.Empty(t, got, typed)
		require.Equal(t, noFiles, directive, typed)
		require.Empty(t, h.api.requests(), "%q made an API call", typed)
	}
}

func TestConfigSetTeamValuesComplete(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald, teamMuster)
	h.serveFolders(teamGruenwald, folderSommerfest, folderPresse)
	h.serveDomains(teamGruenwald, domainVerified, domainPending)

	got, directive := h.complete("config", "set", "team", "")
	require.Equal(t, []string{"sv-gruenwald\tSV Grünwald", "tc-muster\tTC Muster"}, got)
	require.Equal(t, noFiles, directive)

	got, _ = h.complete("config", "set", "team", "tc")
	require.Equal(t, []string{"tc-muster\tTC Muster"}, got)

	// The command refuses folder and domain without --team and has no default
	// team to fall back on, so Tab does not suggest what would then fail.
	calls := len(h.api.requests())
	for _, key := range []string{"folder", "domain"} {
		got, directive = h.complete("config", "set", key, "")
		require.Empty(t, got, key)
		require.Equal(t, noFiles, directive, key)
	}
	require.Len(t, h.api.requests(), calls, "no request was made without --team")

	// With --team, either side of the key.
	for _, words := range [][]string{
		{"config", "set", "folder", "--team", "sv-gruenwald", ""},
		{"config", "set", "--team", "sv-gruenwald", "folder", ""},
	} {
		got, directive = h.complete(words...)
		require.Equal(t, []string{"Sommerfest 2026", "Presse"}, got, words)
		require.Equal(t, noFiles, directive, words)
	}
	got, _ = h.complete("config", "set", "domain", "--team", "sv-gruenwald", "")
	require.Equal(t, []string{"links.sv.de", "go.kurze-url.app"}, got)
}

// Folder, domain and slug completion offer nothing for a team the person is
// not in, and ask nothing about it: another team's values must not leak, and
// the API would refuse the request anyway.
func TestUnknownTeamGivesNothing(t *testing.T) {
	cases := []struct {
		name   string
		config string
		words  []string
	}{
		{"folder, team on the line", "team: sv-gruenwald\n", []string{"link", "list", "--team", "nope", "--folder", ""}},
		{"folder, team in the config", "team: nope\n", []string{"link", "list", "--folder", ""}},
		{"domain", "team: sv-gruenwald\n", []string{"link", "create", "https://example.org", "--team", "nope", "--domain", ""}},
		{"slug", "team: sv-gruenwald\n", []string{"link", "qr", "--team", "nope", "somm"}},
		{"config set folder", "", []string{"config", "set", "folder", "--team", "nope", ""}},
	}
	for _, c := range cases {
		h := linkHarness(t, c.config, teamGruenwald)

		got, directive := h.complete(c.words...)

		require.Empty(t, got, c.name)
		require.Equal(t, noFiles, directive, c.name)
		requireOnlyMe(t, h)
	}
}

// ---- when completion cannot answer ----

func TestCompletionIsSilentWhenSignedOut(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("team: sv-gruenwald\n")

	for _, words := range [][]string{
		{"link", "list", "--team", ""},
		{"link", "list", "--folder", ""},
		{"link", "create", "https://example.org", "--domain", ""},
		{"link", "qr", "somm"},
		{"config", "set", "team", ""},
	} {
		got, directive := h.complete(words...)
		require.Empty(t, got, words)
		require.Equal(t, noFiles, directive, words)
	}

	require.Empty(t, h.opened, "a Tab press never opens the browser")
	require.NotContains(t, h.stderr.String(), "kurze-url:")
	require.Empty(t, h.api.requests(), "no token, so no request")
	require.Empty(t, h.auth.callsTo(stubTokenPath), "and no refresh")
}

func TestCompletionIsSilentOnAPIErrors(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.api.problem(http.MethodGet, apiclient.PathMe, http.StatusInternalServerError, "the database is down")

	for _, words := range [][]string{
		{"link", "list", "--team", ""},
		{"link", "list", "--folder", ""},
	} {
		got, directive := h.complete(words...)
		require.Empty(t, got, words)
		require.Equal(t, noFiles, directive, words)
	}
	require.NotContains(t, h.stderr.String(), "kurze-url:")
}

// An error from the list that follows /v1/me is the same silence.
func TestCompletionIsSilentWhenTheListFails(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.api.problem(http.MethodGet, teamPath(teamGruenwald, "folders"), http.StatusForbidden, "no")
	h.api.problem(http.MethodGet, teamPath(teamGruenwald, "links"), http.StatusInternalServerError, "no")

	for _, words := range [][]string{
		{"link", "list", "--folder", ""},
		{"link", "qr", "somm"},
	} {
		got, directive := h.complete(words...)
		require.Empty(t, got, words)
		require.Equal(t, noFiles, directive, words)
	}
	require.NotContains(t, h.stderr.String(), "kurze-url:")
}

func TestCompletionRefreshesAnExpiredToken(t *testing.T) {
	h := linkHarness(t, "", teamGruenwald, teamMuster)
	require.NoError(t, h.store.Save(credentials.Tokens{
		AccessToken:  "expired-access",
		RefreshToken: signedInRefresh,
		ExpiresAt:    testNow.Add(-time.Minute),
	}))

	got, directive := h.complete("link", "list", "--team", "")

	require.Equal(t, []string{"sv-gruenwald\tSV Grünwald", "tc-muster\tTC Muster"}, got)
	require.Equal(t, noFiles, directive)
	require.Len(t, h.auth.callsTo(stubTokenPath), 1, "the stored refresh token was spent once")
	tokens, err := h.store.Load()
	require.NoError(t, err)
	require.NotEqual(t, signedInRefresh, tokens.RefreshToken, "the rotated token was saved")
	calls := h.api.callsTo(http.MethodGet, apiclient.PathMe)
	require.Len(t, calls, 1)
	require.Equal(t, "Bearer "+tokens.AccessToken, calls[0].Header.Get("Authorization"))
	require.Empty(t, h.opened)
	require.NotContains(t, h.stderr.String(), "kurze-url:")
}

// The budget covers the request too: an API that hangs does not hold the shell.
// The bound is a second and not a few multiples of the 100 ms budget, because
// the test runs under -race on a busy machine.
func TestCompletionGivesUpAtTheBudget(t *testing.T) {
	h := linkHarness(t, "", teamGruenwald)
	h.deps.CompletionTimeout = 100 * time.Millisecond
	h.api.handle(http.MethodGet, apiclient.PathMe, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})

	start := time.Now()
	got, directive := h.complete("link", "list", "--team", "")
	elapsed := time.Since(start)

	require.Empty(t, got)
	require.Equal(t, noFiles, directive)
	require.Less(t, elapsed, time.Second, "completion waited for an API that was not answering")
	require.NotContains(t, h.stderr.String(), "kurze-url:")
}

// ---- text that came from outside ----

// Team names, folder names and destinations are written by other people, and
// a candidate reaches the shell's menu and so the terminal. The completion
// protocol is also line-based, with a tab before the description, so a name
// holding either would forge candidates.
func TestCandidatesCarryNoControlCharacters(t *testing.T) {
	hostileTeam := apiclient.Membership{TeamID: teamGruenwald.TeamID, Name: hostileName, Slug: "sv-gruenwald", Role: "owner"}
	h := linkHarness(t, "team: sv-gruenwald\n", hostileTeam)
	h.serveFolders(hostileTeam,
		apiclient.Folder{ID: "f1", Name: hostileName},
		apiclient.Folder{ID: "f2", Name: "two\nlines"},
		apiclient.Folder{ID: "f3", Name: "tab\tseparated"},
		folderPresse,
	)
	h.serveLinks(hostileTeam, listedLink("sommerfest", "https://example.org/"+hostileName, "active"))

	teams, _ := h.complete("link", "list", "--team", "")
	// Cobra trims a candidate's trailing space, which is where the BEL went.
	require.Equal(t, []string{"sv-gruenwald\t" + strings.TrimSpace(hostileCleaned)}, teams,
		"the description is cleaned the way every other name is")

	// A folder whose own name is not safe to type is not offered at all:
	// a cleaned copy would name a folder that does not exist.
	folders, _ := h.complete("link", "list", "--folder", "")
	require.Equal(t, []string{"Presse"}, folders)

	slugs, _ := h.complete("link", "qr", "somm")
	require.Equal(t, []string{"sommerfest\thttps://example.org/" + strings.TrimSpace(hostileCleaned)}, slugs)
}

// A value lands on the command line, which bash reads as code, and of several
// candidates cobra's bash script inserts the part they share without escaping
// it. Two folders anybody in the team can create would otherwise put live
// syntax on the person's line: a command substitution, a ";" that ends the
// command so the rest runs on the next Enter, a redirection. So a value
// holding any of shellSyntax is not offered.
func TestCandidatesCarryNoShellSyntax(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveFolders(teamGruenwald,
		apiclient.Folder{ID: "f1", Name: "x$(touch pwned)a"},
		apiclient.Folder{ID: "f2", Name: "x$(touch pwned)b"},
		apiclient.Folder{ID: "f3", Name: "`id`"},
		apiclient.Folder{ID: "f4", Name: `back\slash`},
		// The pair the re-review found: bash inserts "x;touch pwned;" and the
		// next Enter runs it.
		apiclient.Folder{ID: "f5", Name: "x;touch pwned;a"},
		apiclient.Folder{ID: "f6", Name: "x;touch pwned;b"},
		apiclient.Folder{ID: "f7", Name: "x&touch pwned&a"},
		apiclient.Folder{ID: "f8", Name: "x|touch pwned|a"},
		apiclient.Folder{ID: "f9", Name: "x>pwned"},
		apiclient.Folder{ID: "f10", Name: "x<pwned"},
		apiclient.Folder{ID: "f11", Name: "x!!"},
		folderPresse,
	)
	h.serveLinks(teamGruenwald, listedLink("sommerfest", "https://example.org/$(id);&|<>!", "active"))

	folders, _ := h.complete("link", "list", "--folder", "")
	require.Equal(t, []string{"Presse"}, folders)

	// A description is shown beside the value and never inserted, so it may
	// hold them.
	slugs, _ := h.complete("link", "qr", "somm")
	require.Equal(t, []string{"sommerfest\thttps://example.org/$(id);&|<>!"}, slugs)
}

// Every character of shellSyntax drops a value, and the set is pinned here
// character by character so that removing one from the constant fails a test
// that names it.
func TestCandidateDropsEachShellSyntaxCharacter(t *testing.T) {
	for _, char := range []string{"$", "`", `\`, ";", "&", "|", "<", ">", "!"} {
		for _, value := range []string{"a" + char + "b", char + "a", "a" + char} {
			_, ok := candidate(value, "")
			require.False(t, ok, "%q was offered", value)
		}
	}
}

// Without a command separator, parentheses, quotes and spaces cannot run
// anything, and a folder like "Sommerfest (2026)" is a name people really use.
func TestCandidateOffersNamesWithoutCommandSyntax(t *testing.T) {
	for _, value := range []string{
		"Sommerfest 2026",
		"Sommerfest (2026)",
		"Kinder-Fest 2026",
		"Vorstand's Ordner",
		`Das "beste" Fest`,
	} {
		got, ok := candidate(value, "")
		require.True(t, ok, "%q was dropped", value)
		require.Equal(t, value, got)
	}
}
