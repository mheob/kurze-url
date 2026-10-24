package cli

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
	"github.com/mheob/kurze-url/apps/cli/internal/output"
)

// reference is what a person typed to name one link, taken apart.
type reference struct {
	// slug is the link's slug, lower-case: the API stores and compares slugs
	// folded, so /SommerFest and /sommerfest are one link.
	slug string
	// host is the hostname of a short URL, lower-case, and "" for a bare slug,
	// which names no host.
	host string
}

// addLookupFlags gives a command the two flags that settle which link a slug
// means: --domain for a slug that exists on several domains, and --team for
// the team to look in. `link stats` and `link qr` both find their link through
// findLink, so they word these flags once.
func addLookupFlags(cmd *cobra.Command, domain, team *string) {
	flags := cmd.Flags()
	flags.StringVar(domain, "domain", "", "the domain of the link, if the slug exists on several")
	flags.StringVar(team, "team", "",
		"look in this team (default: your default team; a full short URL is looked up in all your teams)")
}

// parseReference takes apart a slug or a short URL.
//
// An argument holding a "/" is a URL, and a missing scheme is not a reason to
// refuse it: people type and paste `go.kurze-url.app/sommerfest` as often as
// the full address, so https:// is assumed. The scheme, the port, the query
// and the fragment play no part in naming a link and are ignored, which is
// also why a copied `…/sommerfest?qr=1`, the address a printed QR code opens,
// finds the link. What is left of the path must be exactly one segment, since
// a short link has no more: `/sommerfest/verify` is the password page of one,
// not a link of its own.
//
// Anything else is a mistake in how the command was typed, a UsageError, and
// it is found before any request is made. An argument with no "/" is a bare
// slug, whatever else it looks like.
func (a *App) parseReference(arg string) (reference, error) {
	text := strings.TrimSpace(arg)
	if !strings.Contains(text, "/") {
		if text == "" {
			return reference{}, a.badReference(arg)
		}
		return reference{slug: strings.ToLower(text)}, nil
	}

	if !strings.Contains(text, "://") {
		text = "https://" + text
	}
	u, err := url.Parse(text)
	if err != nil || u.Hostname() == "" {
		return reference{}, a.badReference(arg)
	}
	segment := strings.Trim(u.Path, "/")
	if segment == "" || strings.Contains(segment, "/") {
		return reference{}, a.badReference(arg)
	}
	return reference{slug: strings.ToLower(segment), host: strings.ToLower(u.Hostname())}, nil
}

// badReference words the refusal of an argument that names no link, with an
// example on this instance's own shared host.
func (a *App) badReference(arg string) error {
	text := a.Cat.T("link.badReference", i18n.Args{"arg": arg, "host": a.Env.SharedHost})
	return &UsageError{Err: errors.New(text)}
}

// teamsToSearch says which teams a lookup has to ask.
//
// A short URL names the link, so without --team the person need not know, or
// remember, which of their teams owns it, and every membership is searched. It
// is the only case that costs a request per team, and it needs no default
// team. A bare slug names no host and could belong to any team, so it is
// looked up in the one team the person points at, --team or else their
// default, which is what `link create` and `link list` do too.
func (a *App) teamsToSearch(ctx context.Context, ref reference, teamFlag string) ([]apiclient.Membership, error) {
	if ref.host != "" && teamFlag == "" {
		me, err := a.me(ctx)
		if err != nil {
			return nil, err
		}
		return me.Memberships, nil
	}
	team, err := a.resolveTeam(ctx, teamFlag)
	if err != nil {
		return nil, err
	}
	return []apiclient.Membership{team}, nil
}

// searchTeam returns the links of one team whose slug is exactly slug.
//
// The API's `q` is a substring match over the slug and the destination, so
// "sommerfest" also brings back "sommerfest-2025" and every link that points
// at a page with that word in it, and a team can have more of those than one
// page holds. The exact one may be on any page, so it asks for the next page
// until the count says it has seen everything, or a page comes back empty,
// which ends a loop whose count the items never reach.
func (a *App) searchTeam(ctx context.Context, team apiclient.Membership, slug string) ([]apiclient.Link, error) {
	var matches []apiclient.Link
	seen := 0
	for page := 1; ; page++ {
		result, err := a.API.ListLinks(ctx, team.TeamID, apiclient.ListLinksQuery{
			Q:       slug,
			Page:    page,
			PerPage: maxListLimit,
		})
		if err != nil {
			return nil, err
		}
		for _, l := range result.Items {
			if strings.EqualFold(l.Slug, slug) {
				matches = append(matches, l)
			}
		}
		seen += len(result.Items)
		if len(result.Items) == 0 || seen >= result.TotalCount {
			return matches, nil
		}
	}
}

// findLink turns what a person typed into one link and the membership that
// owns it.
//
// The slug is searched in the teams teamsToSearch names. A link counts when
// its slug equals the typed one ignoring case and its hostname equals every
// host the person gave: the one in a short URL and the one of --domain, which
// are the same host or no link at all. The domain a team has configured plays
// no part: it is where `link create` puts a link by default, and as a filter
// it would hide a link on another domain with nothing on screen to say why.
//
// Exactly one match is the answer. Several, which a bare slug can have across
// domains, are a failure that lists the candidates, because only the person
// knows which they meant; none is a not-found error.
//
// The returned membership is the team that owns the link, found by the link's
// own team_id, and not the team that was searched or the default one. Callers
// that read a team's settings, `link qr` for its colours, read the owner's.
func (a *App) findLink(ctx context.Context, arg, domainFlag, teamFlag string) (apiclient.Link, apiclient.Membership, error) {
	ref, err := a.parseReference(arg)
	if err != nil {
		return apiclient.Link{}, apiclient.Membership{}, err
	}
	teams, err := a.teamsToSearch(ctx, ref, teamFlag)
	if err != nil {
		return apiclient.Link{}, apiclient.Membership{}, err
	}

	var hosts []string
	for _, h := range []string{ref.host, strings.TrimSpace(domainFlag)} {
		if h != "" {
			hosts = append(hosts, h)
		}
	}

	var found []apiclient.Link
	for _, team := range teams {
		links, err := a.searchTeam(ctx, team, ref.slug)
		if err != nil {
			return apiclient.Link{}, apiclient.Membership{}, err
		}
		for _, l := range links {
			if onEveryHost(l, hosts) {
				found = append(found, l)
			}
		}
	}

	switch len(found) {
	case 0:
		return apiclient.Link{}, apiclient.Membership{},
			errors.New(a.Cat.T("link.notFound", i18n.Args{"slug": ref.slug}))
	case 1:
		owner, err := a.owningMembership(ctx, found[0])
		return found[0], owner, err
	}

	// Each candidate is cleaned before the lines are joined, because the
	// newline between them is meant and a control character inside one is not.
	candidates := make([]string, 0, len(found))
	for _, l := range found {
		candidates = append(candidates, output.Clean(l.ShortURL))
	}
	return apiclient.Link{}, apiclient.Membership{}, errors.New(a.Cat.T("link.ambiguous", i18n.Args{
		"slug":       ref.slug,
		"candidates": strings.Join(candidates, "\n"),
	}))
}

// onEveryHost reports whether the link's hostname is each of hosts, ignoring
// case. With no host given every link is.
func onEveryHost(l apiclient.Link, hosts []string) bool {
	for _, h := range hosts {
		if !strings.EqualFold(l.Hostname, h) {
			return false
		}
	}
	return true
}

// owningMembership finds the membership whose team the link belongs to.
//
// Every link a lookup returns came out of a team the person is in, so this
// finds one. The answer for a link whose team is not among the memberships is
// the generic not-found, which is what that would look like to the person
// whatever caused it.
func (a *App) owningMembership(ctx context.Context, l apiclient.Link) (apiclient.Membership, error) {
	me, err := a.me(ctx)
	if err != nil {
		return apiclient.Membership{}, err
	}
	for _, m := range me.Memberships {
		if m.TeamID == l.TeamID {
			return m, nil
		}
	}
	return apiclient.Membership{}, errors.New(a.Cat.T("errors.notFound"))
}
