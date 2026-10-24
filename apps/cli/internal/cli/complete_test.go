package cli

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
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
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fn := completeWith(h.deps, func(context.Context, *App, *cobra.Command, []string, string) ([]cobra.Completion, error) {
		<-release // ignores its context, like a locked keychain
		return []cobra.Completion{"too-late"}, nil
	})

	start := time.Now()
	values, directive := fn(&cobra.Command{}, nil, "")

	require.Nil(t, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Less(t, time.Since(start), 5*time.Second, "completion waited for work that ignores its deadline")
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
