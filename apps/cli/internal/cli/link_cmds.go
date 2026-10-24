package cli

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/config"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
	"github.com/mheob/kurze-url/apps/cli/internal/output"
)

const (
	// defaultListLimit is how many links `link list` shows when --limit is
	// not given, and maxListLimit is the most the API serves in one page.
	// There is no paging beyond that page: the list answers "what did I make
	// lately", and the web app is where a team's whole archive is browsed.
	defaultListLimit = 20
	maxListLimit     = 100

	// linkListSort puts the newest link first, which is what "recent" means.
	linkListSort = "-created_at"

	// linkDestinationColumn is the column of `link list` that gives way when
	// a line is wider than the terminal: the destination is the one
	// free-form, unbounded text in a row, and the short URL and the state
	// are what a person acts on.
	linkDestinationColumn = 1
)

// newLinkCmd is the `link` group.
func newLinkCmd(d *Deps) *cobra.Command {
	link := newGroup("link", "Create and inspect short links")
	link.AddCommand(
		newLinkCreateCmd(d),
		newLinkListCmd(d),
		newLinkStatsCmd(d),
		newLinkQRCmd(d),
	)
	return link
}

// createOptions are the flags of `link create`.
type createOptions struct {
	slug, folder, domain, team string
}

// newLinkCreateCmd creates one short link and prints its short URL.
func newLinkCreateCmd(d *Deps) *cobra.Command {
	var o createOptions
	cmd := &cobra.Command{
		Use:   "create <url>",
		Short: "Create a short link and print its short URL",
		Long: "Creates a short link for <url>, which must be a complete https:// address, and prints\n" +
			"the short URL and nothing else, so the output can go straight into a pipe:\n\n" +
			"  kurze-url link create https://example.org/sommerfest | pbcopy\n\n" +
			"The folder and the domain default to the ones set for the team with `config set`.\n" +
			"A folder that does not exist is an error and is never created. A domain must be one\n" +
			"of the team's verified domains, or the shared domain.",
		Args:              usageArgs(cobra.ExactArgs(1)),
		ValidArgsFunction: noValues,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.linkCreate(cmd.Context(), args[0], o)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&o.slug, "slug", "", "the link's path, such as sommerfest (default: generated)")
	flags.StringVar(&o.folder, "folder", "", "put the link in this folder (default: the team's configured folder)")
	flags.StringVar(&o.domain, "domain", "",
		"use this domain (default: the team's configured domain, else the shared domain)")
	flags.StringVar(&o.team, "team", "", "create the link in this team (default: your default team)")
	registerFlagCompletion(cmd, "slug", noValues)
	return cmd
}

// listOptions are the flags of `link list`.
type listOptions struct {
	folder, search, team string
	limit                int
}

// newLinkListCmd lists the team's most recent links.
func newLinkListCmd(d *Deps) *cobra.Command {
	o := listOptions{limit: defaultListLimit}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your team's most recent links",
		Long: "Lists the newest links of a team, with their destination and state.\n\n" +
			"On a terminal the columns are aligned and a long destination is shortened to fit.\n" +
			"When piped, every row is tab-separated and nothing is shortened. The team's\n" +
			"configured folder is not a filter here: only --folder narrows the list.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			// A flag outside its range is a mistake in how the command was
			// typed, whatever the config file looks like, so it is judged
			// before the file is read.
			if err := checkLimit(d, o.limit); err != nil {
				return err
			}
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.linkList(cmd.Context(), o)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&o.folder, "folder", "", "only links in this folder")
	flags.StringVar(&o.search, "search", "", "only links matching this text")
	flags.IntVar(&o.limit, "limit", defaultListLimit, "how many links to show, from 1 to 100")
	flags.StringVar(&o.team, "team", "", "list the links of this team (default: your default team)")
	registerFlagCompletion(cmd, "search", noValues)
	registerFlagCompletion(cmd, "limit", noValues)
	return cmd
}

// checkLimit refuses a --limit the API would not serve as a usage error, in
// the person's language.
//
// It words the refusal itself because it runs before an App exists, and
// catalogFor is what picks the language when none does.
func checkLimit(d *Deps, limit int) error {
	if limit >= 1 && limit <= maxListLimit {
		return nil
	}
	text := catalogFor(d).T("link.limitRange", i18n.Args{"value": strconv.Itoa(limit)})
	return &UsageError{Err: errors.New(text)}
}

// checkDestination refuses what is not a complete https:// address before
// anything is sent. The API refuses it too, with a 422, but the same words
// without a login, a round trip or the memberships lookup are worth the
// duplicate check: a typo is the likeliest mistake of the whole command.
//
// The API is still the authority on what it accepts (it looks the host up,
// for one), so this only rejects what can never be valid: no scheme, a scheme
// other than https, or no host to go to. The scheme is compared as
// url.Parse reads it, which is lower-cased.
func (a *App) checkDestination(destination string) error {
	u, err := url.Parse(destination)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return errors.New(a.Cat.T("errors.invalidDestination"))
	}
	return nil
}

// settingOr returns flag when the person gave one, else the team's configured
// value of k, else "". It is how --folder and --domain fall back to the
// config.
func (a *App) settingOr(flag string, k config.Key, team apiclient.Membership) string {
	if flag != "" {
		return flag
	}
	value, _, _ := a.Config.Resolve(k, team.Slug)
	return value
}

// linkCreate makes one link and prints its short URL.
//
// Everything that can be wrong without the API's say-so is settled first, in
// the order a person would fix it: the destination, the team, the folder, the
// domain. The request is sent last and exactly once; CreateLink never
// retries, because a request that died on the wire may still have created the
// link.
//
// The short URL is the only thing written to stdout. Whatever else there is to
// say, a stale team section in the config or a keychain note, goes to stderr
// before this point, so `$(kurze-url link create …)` and a pipe both read
// exactly the URL.
func (a *App) linkCreate(ctx context.Context, destination string, o createOptions) error {
	if err := a.checkDestination(destination); err != nil {
		return err
	}
	team, err := a.resolveTeam(ctx, o.team)
	if err != nil {
		return err
	}
	folderID, err := a.resolveFolder(ctx, team, a.settingOr(o.folder, config.Folder, team))
	if err != nil {
		return err
	}
	domainID, err := a.resolveDomain(ctx, team, a.settingOr(o.domain, config.Domain, team))
	if err != nil {
		return err
	}

	link, err := a.API.CreateLink(ctx, team.TeamID, apiclient.CreateLinkRequest{
		DestinationURL: destination,
		Slug:           o.slug,
		FolderID:       folderID,
		DomainID:       domainID,
	})
	if err != nil {
		return a.describeCreateFailure(err)
	}
	return a.result(output.Clean(link.ShortURL))
}

// describeCreateFailure words the one answer that only this command knows the
// meaning of. On link creation a 409 can only be a slug that is already taken
// on that domain, which describe cannot assume for a 409 in general (a PATCH
// of a blocked link answers one too), so the mapping lives here and not in
// describe. Anything else goes back unchanged for describe to word.
//
// The result is deliberately not wrapped around the API's error: describe
// reads a *ProblemError anywhere in a chain first and would print its generic
// text instead of this one.
func (a *App) describeCreateFailure(err error) error {
	var problem *apiclient.ProblemError
	if errors.As(err, &problem) && problem.Status == http.StatusConflict {
		return errors.New(a.Cat.T("link.slugTaken"))
	}
	return err
}

// linkList prints one page of the team's newest links.
//
// A terminal gets an aligned table under a localised header, with the
// destination shortened to fit. Anything else gets bare tab-separated rows
// and no header, like `team list`, so a script reads the first line as data.
// With nothing to show it says so on stderr and prints nothing at all: not
// even a header, which a script would read as a row.
//
// The team's configured folder plays no part. It is a default for creating a
// link, and as a filter it would hide links with nothing on screen to say
// why; only --folder narrows the list.
func (a *App) linkList(ctx context.Context, o listOptions) error {
	team, err := a.resolveTeam(ctx, o.team)
	if err != nil {
		return err
	}
	folderID, err := a.resolveFolder(ctx, team, o.folder)
	if err != nil {
		return err
	}

	page, err := a.API.ListLinks(ctx, team.TeamID, apiclient.ListLinksQuery{
		Q:        o.search,
		FolderID: folderID,
		Sort:     linkListSort,
		Page:     1,
		PerPage:  o.limit,
	})
	if err != nil {
		return err
	}
	if len(page.Items) == 0 {
		a.notice(a.Cat.T("link.noneFound"))
		return nil
	}

	rows := make([][]string, 0, len(page.Items))
	for _, l := range page.Items {
		rows = append(rows, []string{l.ShortURL, l.DestinationURL, stateLabel(a.Cat, l.State)})
	}

	if a.d.StdoutTTY() {
		header := []string{
			a.Cat.T("link.header.shortUrl"),
			a.Cat.T("link.header.destination"),
			a.Cat.T("link.header.state"),
		}
		output.Table(a.d.Stdout, header, rows, a.d.TermWidth(), linkDestinationColumn)
		return nil
	}
	output.TSV(a.d.Stdout, rows)
	return nil
}

// stateLabel words a link's state the way the web app does: "flagged" reads
// "blocked" in English and "gesperrt" in German, because to the person it is
// a link that is blocked, whoever blocked it. A state the CLI does not know
// is shown as the API sent it, so a state added to the API shows up before the
// CLI learns its name.
//
// Every case calls T with its own literal key, which is what the catalog check
// needs to see.
func stateLabel(c *i18n.Catalog, state string) string {
	switch state {
	case "active":
		return c.T("link.state.active")
	case "disabled":
		return c.T("link.state.disabled")
	case "expired":
		return c.T("link.state.expired")
	case "flagged":
		return c.T("link.state.flagged")
	}
	return state
}
