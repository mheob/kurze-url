package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// completionTimeout is the one deadline a Tab press gets, for building the
// App, refreshing a token and every request together. Completion runs on a
// person's keystroke, and a shell that stalls for longer than this feels
// broken, so what has not arrived by then is not offered.
const completionTimeout = 2 * time.Second

// noValues is a completion that offers nothing and, unlike no function at all,
// says so: the shell would otherwise fall back to file names, which is wrong
// for a slug, a number or a URL. It is cobra's own NoFileCompletions, named for
// what it reads like at the place that registers it.
func noValues(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return cobra.NoFileCompletions(cmd, args, toComplete)
}

// fixedValues is a completion that always offers the same values and no file
// names. It does not filter by what was typed: the shell matches the
// candidates against the word it is completing, and the values here are too few
// for that to cost anything.
func fixedValues(values ...string) cobra.CompletionFunc {
	return cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp)
}

// withPrefix keeps the candidates that start with what has been typed, for a
// completion that builds its own list and has to answer the way a shell
// matching it would. Only the value is compared: the text after the tab is the
// description a shell shows beside it, and typing part of a team's name must
// not select it.
//
// The comparison folds case on both sides. Keys, slugs and domains are
// lower-case by rule, so a word typed with a capital can only mean the
// lower-case one, and it is the same folding resolveTeam applies to a team
// typed in full.
func withPrefix(values []cobra.Completion, typed string) []cobra.Completion {
	typed = strings.ToLower(typed)
	var kept []cobra.Completion
	for _, v := range values {
		value, _, _ := strings.Cut(v, "\t")
		if strings.HasPrefix(strings.ToLower(value), typed) {
			kept = append(kept, v)
		}
	}
	return kept
}

// registerFlagCompletion offers completion for one flag's value. A flag that
// does not exist, or one registered twice, is a mistake in the command's own
// constructor and not something a person can cause, so it stops the program at
// once: every test builds the whole tree, and one such mistake fails them all
// on the line that made it.
func registerFlagCompletion(cmd *cobra.Command, flag string, complete cobra.CompletionFunc) {
	if err := cmd.RegisterFlagCompletionFunc(flag, complete); err != nil {
		panic("register completion for --" + flag + " of " + cmd.CommandPath() + ": " + err.Error())
	}
}

// completeWith is a completion that has to ask the service: it builds an App
// the way a command does and hands it to list, which returns the candidates.
//
// Completion is silent, fast and never interactive, and this is where that is
// enforced, so no list function has to remember it.
//
//   - The App is built from a muted copy of the Deps. Its streams discard
//     everything, because whatever a command prints would land in the
//     shell's candidate list or on the person's prompt; stdin is empty and not
//     a terminal, so nothing can ask a question; and the browser opener
//     refuses, so a login can never start from a Tab press. The caller's Deps
//     is left as it was.
//   - The work runs on a goroutine of its own, under one deadline of
//     Deps.CompletionTimeout, and the answer is whichever comes first. A
//     keychain read does not honour a context and a locked keychain can block
//     it indefinitely, so waiting for the goroutine would hang the shell: at
//     the deadline the answer is empty, whatever is still running.
//   - An error, a panic, an App that cannot be built (a broken config, a bad
//     environment override) and a timeout are all the same answer: nothing,
//     and no file names. None of them is reported. The command being typed
//     words a broken config or a lost session itself when it runs, and a
//     message printed during Tab is the interruption this exists to avoid.
//
// The directive is always ShellCompDirectiveNoFileComp, so an empty answer is
// not turned into a list of files by zsh.
func completeWith(
	d *Deps,
	list func(ctx context.Context, a *App, cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, error),
) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		muted := *d
		muted.Stdout = io.Discard
		muted.Stderr = io.Discard
		muted.Stdin = strings.NewReader("")
		muted.StdinTTY = func() bool { return false }
		muted.OpenBrowser = func(string) error { return errors.New("completion never opens a browser") }

		ctx, cancel := context.WithTimeout(context.Background(), d.CompletionTimeout)
		defer cancel()

		// The channel holds one answer, so a goroutine that finishes after
		// the deadline still ends instead of waiting for a reader.
		type answer struct {
			values []cobra.Completion
			err    error
		}
		done := make(chan answer, 1)
		go func() {
			defer func() {
				if recover() != nil {
					done <- answer{err: errors.New("completion panicked")}
				}
			}()
			a, err := newApp(&muted)
			if err != nil {
				done <- answer{err: err}
				return
			}
			values, err := list(ctx, a, cmd, args, toComplete)
			done <- answer{values: values, err: err}
		}()

		select {
		case got := <-done:
			if got.err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return got.values, cobra.ShellCompDirectiveNoFileComp
		case <-ctx.Done():
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
}
