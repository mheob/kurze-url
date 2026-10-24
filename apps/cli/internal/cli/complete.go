package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/output"
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

// candidate builds one completion out of text the service sent, or reports
// false for a value that cannot be offered.
//
// A team's name, a folder's name and a link's destination are written by other
// people, and a candidate ends up in the shell's menu and so on the terminal,
// where an escape sequence would be obeyed (output.Clean says which). The
// protocol is also made of lines and tabs: cobra keeps the first line of a
// candidate, so a name with a newline would be offered cut short, and a tab in
// a value would start a description of somebody else's choosing. The
// description is cleaned like any other text the CLI shows. A value is not
// cleaned but dropped when it has a control character in it: the cleaned copy
// would name a team or folder that does not exist, and typing it would fail
// where offering nothing would not.
//
// An empty description is no description, so a shell does not draw a bare
// separator.
func candidate(value, description string) (cobra.Completion, bool) {
	if value == "" || output.Clean(value) != value {
		return "", false
	}
	if description == "" {
		return value, true
	}
	return cobra.CompletionWithDesc(value, output.Clean(description)), true
}

// completionTeam is the team a dynamic completion looks values up in: the
// --team already on the command line, else, when useDefault, the default team
// from the config. That is resolveTeam's rule, minus the failures: where
// resolveTeam words a refusal, this says "no team" and the completion offers
// nothing.
//
// The slug is folded and matched against the person's memberships, so a team
// they are not in gives false, with one request for the memberships and none
// for the team's own values. Offering nothing for it is deliberate: the API
// would refuse the request, and there is no other team's data to fall back on.
// With no team at all it gives false without asking anything.
//
// useDefault is false for `config set folder` and `config set domain`: the
// command itself refuses those keys without --team and has no default-team
// fallback, so a Tab that listed the default team's values would suggest a
// command that then fails.
func completionTeam(ctx context.Context, a *App, cmd *cobra.Command, useDefault bool) (apiclient.Membership, bool) {
	// A command without the flag reads as "not given".
	typed, _ := cmd.Flags().GetString("team")
	if typed == "" && useDefault {
		typed = a.configuredTeam()
	}
	if typed == "" {
		return apiclient.Membership{}, false
	}
	me, err := a.me(ctx)
	if err != nil {
		return apiclient.Membership{}, false
	}
	return membershipOf(me, strings.ToLower(typed))
}

// completeTeams completes a team: the slugs of the person's memberships, each
// with the team's name beside it. It needs no team of its own, only a session.
func completeTeams(d *Deps) cobra.CompletionFunc {
	return completeWith(d, func(ctx context.Context, a *App, _ *cobra.Command, _ []string, typed string) ([]cobra.Completion, error) {
		me, err := a.me(ctx)
		if err != nil {
			return nil, err
		}
		var teams []cobra.Completion
		for _, m := range me.Memberships {
			if c, ok := candidate(m.Slug, m.Name); ok {
				teams = append(teams, c)
			}
		}
		return withPrefix(teams, typed), nil
	})
}

// completeFolders completes a folder: the names of the folders of
// completionTeam's team, as the one word each is, spaces and all.
func completeFolders(d *Deps, useDefault bool) cobra.CompletionFunc {
	return completeWith(d, func(ctx context.Context, a *App, cmd *cobra.Command, _ []string, typed string) ([]cobra.Completion, error) {
		team, ok := completionTeam(ctx, a, cmd, useDefault)
		if !ok {
			return nil, nil
		}
		folders, err := a.API.Folders(ctx, team.TeamID)
		if err != nil {
			return nil, err
		}
		var names []cobra.Completion
		for _, f := range folders {
			if c, ok := candidate(f.Name, ""); ok {
				names = append(names, c)
			}
		}
		return withPrefix(names, typed), nil
	})
}

// completeDomains completes a domain: the hostnames of completionTeam's team
// that have been verified, then the instance's shared host.
//
// Only a verified domain may carry a link, and `link create` refuses any other
// by name (resolveDomain), so offering one would suggest a command that then
// fails. The shared host is not one of the team's domains and the team never
// lists it, but it is the one `--domain` always accepts, so it comes last.
func completeDomains(d *Deps, useDefault bool) cobra.CompletionFunc {
	return completeWith(d, func(ctx context.Context, a *App, cmd *cobra.Command, _ []string, typed string) ([]cobra.Completion, error) {
		team, ok := completionTeam(ctx, a, cmd, useDefault)
		if !ok {
			return nil, nil
		}
		domains, err := a.API.Domains(ctx, team.TeamID)
		if err != nil {
			return nil, err
		}
		var hosts []cobra.Completion
		for _, domain := range domains {
			if domain.VerificationStatus != verifiedStatus {
				continue
			}
			if c, ok := candidate(domain.Hostname, ""); ok {
				hosts = append(hosts, c)
			}
		}
		if c, ok := candidate(a.Env.SharedHost, ""); ok {
			hosts = append(hosts, c)
		}
		return withPrefix(hosts, typed), nil
	})
}

// completeLinkSlugs completes the one link a command takes: the slugs of the
// default team's (or --team's) links that start with what has been typed, each
// with its destination beside it.
//
// The API's q is a substring match over slug and destination, so it is sent
// the typed text to narrow the page, and the answer is filtered again for
// slugs that begin with it. One page of the API's maximum is enough for a
// completion; a team with more links than that gets the slugs of the page and
// types the rest. The same slug on two domains is offered once, because which
// of them is meant is for --domain to say.
//
// A word holding a "/" or a ":" is a short URL being typed. Short URLs are not
// completed, and neither the team nor the API is asked about one. A word after
// the first has no slug to offer, as the command takes one link.
func completeLinkSlugs(d *Deps) cobra.CompletionFunc {
	slugs := completeWith(d, func(ctx context.Context, a *App, cmd *cobra.Command, _ []string, typed string) ([]cobra.Completion, error) {
		team, ok := completionTeam(ctx, a, cmd, true)
		if !ok {
			return nil, nil
		}
		page, err := a.API.ListLinks(ctx, team.TeamID, apiclient.ListLinksQuery{Q: typed, PerPage: maxListLimit})
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		var links []cobra.Completion
		for _, l := range page.Items {
			if seen[l.Slug] {
				continue
			}
			if c, ok := candidate(l.Slug, l.DestinationURL); ok {
				seen[l.Slug] = true
				links = append(links, c)
			}
		}
		return withPrefix(links, typed), nil
	})
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 || strings.ContainsAny(toComplete, "/:") {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return slugs(cmd, args, toComplete)
	}
}
