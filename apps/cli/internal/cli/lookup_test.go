package cli

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
)

// The links the lookup tests search. Their IDs are explicit and distinct,
// because listedLink derives an ID from string lengths and two of them would
// collide.
var (
	// The same slug twice in team A, on the shared host and on the team's own
	// domain: the case a bare slug cannot settle.
	lookupShared = lookupLink(0xa1, teamGruenwald, "go.kurze-url.app", "sommerfest")
	lookupCustom = lookupLink(0xa2, teamGruenwald, "links.sv-gruenwald.de", "sommerfest")
	// A link in team B, which is not the default team of these tests.
	lookupFest = lookupLink(0xb1, teamMuster, "go.kurze-url.app", "fest")
)

// lookupLink is a link as the list endpoint returns it, with the team it
// belongs to, which is what tells a lookup who owns it.
func lookupLink(id int, team apiclient.Membership, host, slug string) apiclient.Link {
	return apiclient.Link{
		ID:             fmt.Sprintf("00000000-0000-4000-8000-%012x", id),
		TeamID:         team.TeamID,
		Hostname:       host,
		Slug:           slug,
		ShortURL:       "https://" + host + "/" + slug,
		DestinationURL: "https://example.org/" + slug,
		State:          "active",
	}
}

// serveLinkSearch makes a team's link list behave like the API's: it keeps the
// links whose slug or destination holds q, in the order given, and serves them
// page by page with the total count of all of them.
//
// A fixed answer would hide what a lookup has to get right, which is asking
// for the next page until the count is reached.
func (h *harness) serveLinkSearch(team apiclient.Membership, links ...apiclient.Link) {
	h.t.Helper()
	h.api.handle(http.MethodGet, teamPath(team, "links"), func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		q := strings.ToLower(query.Get("q"))
		page, perPage := queryInt(query.Get("page"), 1), queryInt(query.Get("per_page"), 20)

		matched := []apiclient.Link{}
		for _, l := range links {
			if strings.Contains(strings.ToLower(l.Slug+" "+l.DestinationURL), q) {
				matched = append(matched, l)
			}
		}
		start := min((page-1)*perPage, len(matched))
		end := min(start+perPage, len(matched))
		writeJSON(w, http.StatusOK, "application/json", apiclient.Page[apiclient.Link]{
			Items: matched[start:end], Page: page, PerPage: perPage, TotalCount: len(matched),
		})
	})
}

// queryInt reads a query parameter as a number, or returns fallback.
func queryInt(text string, fallback int) int {
	n, err := strconv.Atoi(text)
	if err != nil {
		return fallback
	}
	return n
}

// lookupHarness is a signed-in person in both teams whose default team is A,
// with the links above in place. Team B's search is registered, so a test can
// see whether it was asked.
func lookupHarness(t *testing.T, config string) *harness {
	t.Helper()
	h := linkHarness(t, config, teamGruenwald, teamMuster)
	h.serveLinkSearch(teamGruenwald, lookupShared, lookupCustom)
	h.serveLinkSearch(teamMuster, lookupFest)
	return h
}

// find runs findLink the way a command would.
func find(t *testing.T, h *harness, arg, domain, team string) (apiclient.Link, apiclient.Membership, error) {
	t.Helper()
	return newTestApp(t, h).findLink(context.Background(), arg, domain, team)
}

// Review Focus 4: one link, many spellings. The slug is stored lower-case on
// the shared host, and every way of typing it must find that link.
func TestFindLinkSpellings(t *testing.T) {
	for _, arg := range []string{
		"sommerfest",
		"SommerFest",
		" sommerfest ",
		"GO.KURZE-URL.APP/sommerfest",
		"go.kurze-url.app/SOMMERFEST",
		"https://go.kurze-url.app/sommerfest",
		"https://go.kurze-url.app/sommerfest/",
		"https://go.kurze-url.app/sommerfest?qr=1",
		"https://go.kurze-url.app/sommerfest/?qr=1#top",
		"http://go.kurze-url.app/sommerfest",
		"HTTPS://Go.Kurze-URL.app/SommerFest",
		"https://go.kurze-url.app:443/sommerfest",
	} {
		t.Run(arg, func(t *testing.T) {
			h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
			// Only the shared-host link exists, so the bare spellings are not
			// ambiguous. The ambiguous case has its own test.
			h.serveLinkSearch(teamGruenwald, lookupShared)

			link, owner, err := find(t, h, arg, "", "")

			require.NoError(t, err)
			require.Equal(t, lookupShared.ID, link.ID)
			require.Equal(t, teamGruenwald, owner)
			// The query is the lower-cased slug, whatever was typed.
			for _, c := range h.api.callsTo(http.MethodGet, teamPath(teamGruenwald, "links")) {
				require.Equal(t, "sommerfest", c.Query.Get("q"))
			}
		})
	}
}

func TestFindLinkBareSlugAcrossDomainsIsAmbiguous(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	_, _, err := find(t, h, "sommerfest", "", "")

	require.EqualError(t, err,
		"More than one link has the slug “sommerfest”. Name the domain with --domain, or give the full short URL:\n"+
			"https://go.kurze-url.app/sommerfest\n"+
			"https://links.sv-gruenwald.de/sommerfest")
}

// Several matches are a failure of this run and not a typo in the command, so
// they exit 1, and the lines of candidates reach stderr whole.
func TestFindLinkAmbiguityIsAFailureThatListsTheCandidates(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	code := h.run("link", "stats", "sommerfest")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Contains(t, h.stderr.String(), "\nhttps://go.kurze-url.app/sommerfest\nhttps://links.sv-gruenwald.de/sommerfest\n")
	require.Empty(t, h.api.callsTo(http.MethodGet, "/v1/links/"+lookupShared.ID+"/stats"))
}

func TestFindLinkDomainFlagDisambiguates(t *testing.T) {
	for host, want := range map[string]apiclient.Link{
		"go.kurze-url.app":      lookupShared,
		"GO.Kurze-URL.app":      lookupShared,
		"links.sv-gruenwald.de": lookupCustom,
	} {
		t.Run(host, func(t *testing.T) {
			h := lookupHarness(t, "team: sv-gruenwald\n")

			link, owner, err := find(t, h, "sommerfest", host, "")

			require.NoError(t, err)
			require.Equal(t, want.ID, link.ID)
			require.Equal(t, teamGruenwald, owner)
		})
	}
}

// The URL already names a host, so it settles the question without --domain.
func TestFindLinkFullURLNamesTheDomain(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	link, _, err := find(t, h, "https://links.sv-gruenwald.de/sommerfest", "", "")

	require.NoError(t, err)
	require.Equal(t, lookupCustom.ID, link.ID)
}

// A URL and --domain that name different hosts describe no link at all.
func TestFindLinkURLAndDomainFlagMustAgree(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	_, _, err := find(t, h, "https://go.kurze-url.app/sommerfest", "links.sv-gruenwald.de", "")
	require.EqualError(t, err, "No link “sommerfest” found.")

	link, _, err := find(t, h, "https://go.kurze-url.app/sommerfest", "go.kurze-url.app", "")
	require.NoError(t, err)
	require.Equal(t, lookupShared.ID, link.ID)
}

// The configured domain is where `link create` puts a link by default. As a
// filter it would hide the link on the shared host, with nothing on screen to
// say why, exactly like the configured folder in `link list`.
func TestFindLinkIgnoresTheConfiguredDomain(t *testing.T) {
	h := lookupHarness(t,
		"team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    domain: links.sv-gruenwald.de\n")

	_, _, err := find(t, h, "sommerfest", "", "")

	require.ErrorContains(t, err, "More than one link has the slug")
}

// The full URL of a link in a team that is not the default one is enough: the
// person does not have to know which team owns it.
func TestFindLinkURLSearchesEveryTeam(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	link, owner, err := find(t, h, "https://go.kurze-url.app/fest", "", "")

	require.NoError(t, err)
	require.Equal(t, lookupFest.ID, link.ID)
	require.Equal(t, teamMuster, owner, "the owner is the team the link belongs to, not the default team")
	require.Len(t, h.api.callsTo(http.MethodGet, teamPath(teamGruenwald, "links")), 1)
	require.Len(t, h.api.callsTo(http.MethodGet, teamPath(teamMuster, "links")), 1)
}

func TestFindLinkURLNeedsNoDefaultTeam(t *testing.T) {
	h := lookupHarness(t, "")

	link, owner, err := find(t, h, "https://go.kurze-url.app/fest", "", "")

	require.NoError(t, err)
	require.Equal(t, lookupFest.ID, link.ID)
	require.Equal(t, teamMuster, owner)
}

// With --team the person said where to look, so nothing else is searched.
func TestFindLinkURLWithTeamFlagSearchesOnlyThatTeam(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	_, _, err := find(t, h, "https://go.kurze-url.app/fest", "", "sv-gruenwald")

	require.EqualError(t, err, "No link “fest” found.")
	require.Empty(t, h.api.callsTo(http.MethodGet, teamPath(teamMuster, "links")))

	link, owner, err := find(t, h, "https://go.kurze-url.app/fest", "", "tc-muster")
	require.NoError(t, err)
	require.Equal(t, lookupFest.ID, link.ID)
	require.Equal(t, teamMuster, owner)
}

// A bare slug names no host, so it is looked up in one team only: the default,
// or the one --team gives.
func TestFindLinkBareSlugSearchesOnlyOneTeam(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	_, _, err := find(t, h, "fest", "", "")

	require.EqualError(t, err, "No link “fest” found.")
	require.Empty(t, h.api.callsTo(http.MethodGet, teamPath(teamMuster, "links")))

	link, owner, err := find(t, h, "fest", "", "tc-muster")
	require.NoError(t, err)
	require.Equal(t, lookupFest.ID, link.ID)
	require.Equal(t, teamMuster, owner)
}

func TestFindLinkBareSlugWithoutATeamSaysSo(t *testing.T) {
	h := lookupHarness(t, "")

	_, _, err := find(t, h, "fest", "", "")

	require.ErrorContains(t, err, "No team given")
	require.Empty(t, h.api.callsTo(http.MethodGet, teamPath(teamMuster, "links")))
}

func TestFindLinkTeamFlagMustBeAMembership(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	_, _, err := find(t, h, "fest", "", "someone-elses")

	require.ErrorContains(t, err, "You are not a member of the team “someone-elses”")
}

// 230 links match "sommerfest" by substring and the API serves 100 a page; the
// exact one is the very last, on page 3.
func TestFindLinkPagesThroughResults(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	var links []apiclient.Link
	for i := range 229 {
		links = append(links, lookupLink(0x1000+i, teamGruenwald, "go.kurze-url.app", fmt.Sprintf("sommerfest-%03d", i)))
	}
	exact := lookupLink(0x2000, teamGruenwald, "go.kurze-url.app", "sommerfest")
	h.serveLinkSearch(teamGruenwald, append(links, exact)...)

	link, _, err := find(t, h, "sommerfest", "", "")

	require.NoError(t, err)
	require.Equal(t, exact.ID, link.ID)
	calls := h.api.callsTo(http.MethodGet, teamPath(teamGruenwald, "links"))
	require.Len(t, calls, 3)
	for i, c := range calls {
		require.Equal(t, strconv.Itoa(i+1), c.Query.Get("page"))
		require.Equal(t, "100", c.Query.Get("per_page"))
		require.Equal(t, "sommerfest", c.Query.Get("q"))
	}
}

// A search that finds nothing at all costs one request and ends, however
// many pages a total count might otherwise suggest.
func TestFindLinkStopsAtAnEmptyPage(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	// A count the items never reach: the loop must end on the empty page.
	h.api.respond(http.MethodGet, teamPath(teamGruenwald, "links"), http.StatusOK, apiclient.Page[apiclient.Link]{
		Items: []apiclient.Link{}, Page: 1, PerPage: 100, TotalCount: 500,
	})

	_, _, err := find(t, h, "sommerfest", "", "")

	require.EqualError(t, err, "No link “sommerfest” found.")
	require.Len(t, h.api.callsTo(http.MethodGet, teamPath(teamGruenwald, "links")), 1)
}

// The API matches by substring, so most of what comes back is not the link.
func TestFindLinkKeepsOnlyTheExactSlug(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald,
		lookupLink(0x31, teamGruenwald, "go.kurze-url.app", "sommerfest-2025"),
		lookupLink(0x32, teamGruenwald, "go.kurze-url.app", "mini-sommerfest"),
	)

	_, _, err := find(t, h, "sommerfest", "", "")

	require.EqualError(t, err, "No link “sommerfest” found.")
}

func TestFindLinkNotFound(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	code := h.run("link", "stats", "https://go.kurze-url.app/gibtesnicht")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t, "kurze-url: No link “gibtesnicht” found.\n", h.stderr.String())
}

func TestFindLinkNotFoundSpeaksGerman(t *testing.T) {
	h := lookupHarness(t, "language: de\nteam: sv-gruenwald\n")

	_, _, err := find(t, h, "gibtesnicht", "", "")

	require.EqualError(t, err, "Kein Link „gibtesnicht“ gefunden.")
}

func TestFindLinkAmbiguityIsWordedInGerman(t *testing.T) {
	h := lookupHarness(t, "language: de\nteam: sv-gruenwald\n")

	_, _, err := find(t, h, "sommerfest", "", "")

	require.EqualError(t, err,
		"Mehrere Links haben den Slug „sommerfest“. Gib die Domain mit --domain an oder die vollständige Kurz-URL:\n"+
			"https://go.kurze-url.app/sommerfest\n"+
			"https://links.sv-gruenwald.de/sommerfest")
}

// An argument that is a URL has to name exactly one path segment. Anything
// else is a mistake in how the command was typed, found before a single
// request: not even /v1/me is asked.
func TestFindLinkRefusesWhatIsNotASlugOrAShortURL(t *testing.T) {
	for _, arg := range []string{
		"https://go.kurze-url.app/",
		"https://go.kurze-url.app",
		"go.kurze-url.app/",
		"https://go.kurze-url.app/sommerfest/verify",
		"https://go.kurze-url.app/a/b/c",
		"/sommerfest",
		"https:///sommerfest",
		"https://go.kurze-url.app:notaport/sommerfest",
		"",
		"   ",
	} {
		t.Run(arg, func(t *testing.T) {
			h := lookupHarness(t, "team: sv-gruenwald\n")

			code := h.run("link", "stats", arg)

			require.Equal(t, 2, code)
			require.Empty(t, h.stdout.String())
			require.Contains(t, h.stderr.String(), "is neither a slug nor a short URL")
			require.Contains(t, h.stderr.String(), "https://go.kurze-url.app/sommerfest")
			require.Contains(t, h.stderr.String(), "Run 'kurze-url link stats --help' for usage.")
			require.Empty(t, h.api.requests())
		})
	}
}

func TestFindLinkRefusalSpeaksGerman(t *testing.T) {
	h := lookupHarness(t, "language: de\nteam: sv-gruenwald\n")

	require.Equal(t, 2, h.run("link", "stats", "https://go.kurze-url.app/a/b"))

	require.Contains(t, h.stderr.String(), "ist weder ein Slug noch eine Kurz-URL")
}

// The usage error is typed, so Run turns it into exit code 2.
func TestFindLinkRefusalIsAUsageError(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\n")

	_, _, err := find(t, h, "https://go.kurze-url.app/a/b", "", "")

	var usage *UsageError
	require.ErrorAs(t, err, &usage)
}
