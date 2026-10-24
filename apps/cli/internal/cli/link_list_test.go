package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
)

// listedLink is a link as the list endpoint returns it.
func listedLink(slug, destination, state string) apiclient.Link {
	return apiclient.Link{
		ID:             "00000000-0000-4000-8000-" + fmt.Sprintf("%012x", len(slug)+len(destination)),
		Hostname:       "go.kurze-url.app",
		Slug:           slug,
		ShortURL:       "https://go.kurze-url.app/" + slug,
		DestinationURL: destination,
		State:          state,
	}
}

// serveLinks makes a team's link list answer with these links as one page.
func (h *harness) serveLinks(team apiclient.Membership, links ...apiclient.Link) {
	h.t.Helper()
	if links == nil {
		links = []apiclient.Link{}
	}
	h.api.respond(http.MethodGet, teamPath(team, "links"), http.StatusOK, apiclient.Page[apiclient.Link]{
		Items: links, Page: 1, PerPage: 20, TotalCount: len(links),
	})
}

// listQuery returns the query of the one list request a team received.
func (h *harness) listQuery(team apiclient.Membership) url.Values {
	h.t.Helper()
	calls := h.api.callsTo(http.MethodGet, teamPath(team, "links"))
	require.Len(h.t, calls, 1)
	return calls[0].Query
}

func TestListSendsSortLimitAndFilters(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveFolders(teamGruenwald, testFolderPlakate, testFolderNewsletter)
	h.serveLinks(teamGruenwald, listedLink("sommerfest", "https://example.org/a", "active"))

	code := h.run("link", "list", "--folder", "NEWSLETTER", "--search", "sommer", "--limit", "5")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, url.Values{
		"sort":      {"-created_at"},
		"per_page":  {"5"},
		"page":      {"1"},
		"q":         {"sommer"},
		"folder_id": {testFolderNewsletter.ID},
	}, h.listQuery(teamGruenwald))
}

// Without flags the newest twenty come back, and no filter is sent at all:
// an empty q or folder_id would be a value the API validates.
func TestListDefaultsToTheNewestTwenty(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald)

	require.Equal(t, 0, h.run("link", "list"), h.stderr.String())

	require.Equal(t, url.Values{
		"sort":     {"-created_at"},
		"per_page": {"20"},
		"page":     {"1"},
	}, h.listQuery(teamGruenwald))
}

func TestListLimitOutOfRangeIsAUsageError(t *testing.T) {
	for _, limit := range []string{"0", "101", "-1", "1000"} {
		t.Run(limit, func(t *testing.T) {
			h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)

			code := h.run("link", "list", "--limit", limit)

			require.Equal(t, 2, code)
			require.Empty(t, h.stdout.String())
			require.Equal(t,
				"kurze-url: --limit must be a whole number from 1 to 100, not "+limit+".\n"+
					"Run 'kurze-url link list --help' for usage.\n",
				h.stderr.String())
			require.Empty(t, h.api.requests())
		})
	}
}

func TestListAcceptsTheLimitsEdges(t *testing.T) {
	for _, limit := range []string{"1", "100"} {
		t.Run(limit, func(t *testing.T) {
			h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
			h.serveLinks(teamGruenwald)

			require.Equal(t, 0, h.run("link", "list", "--limit", limit), h.stderr.String())

			require.Equal(t, []string{limit}, h.listQuery(teamGruenwald)["per_page"])
		})
	}
}

func TestListLimitMustBeANumber(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)

	require.Equal(t, 2, h.run("link", "list", "--limit", "many"))
	require.Empty(t, h.api.requests())
}

func TestListLimitErrorSpeaksGerman(t *testing.T) {
	h := linkHarness(t, "language: de\nteam: sv-gruenwald\n", teamGruenwald)

	require.Equal(t, 2, h.run("link", "list", "--limit", "0"))

	require.Contains(t, h.stderr.String(), "--limit muss eine ganze Zahl von 1 bis 100 sein, nicht 0.")
}

func TestListPrintsTSVWhenPiped(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald,
		listedLink("sommerfest", "https://example.org/sommerfest", "active"),
		listedLink("alt", "https://example.org/alt", "expired"),
		listedLink("pause", "https://example.org/pause", "disabled"),
	)

	code := h.run("link", "list")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t,
		"https://go.kurze-url.app/sommerfest\thttps://example.org/sommerfest\tactive\n"+
			"https://go.kurze-url.app/alt\thttps://example.org/alt\texpired\n"+
			"https://go.kurze-url.app/pause\thttps://example.org/pause\tdisabled\n",
		h.stdout.String())
	require.Empty(t, h.stderr.String())
}

// A script reading a pipe gets every character of the destination, however
// narrow the terminal it happens to be started from.
func TestListNeverShortensAPipe(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	destination := "https://example.org/" + strings.Repeat("x", 120)
	h.serveLinks(teamGruenwald, listedLink("lang", destination, "active"))
	h.termWidth = 40

	require.Equal(t, 0, h.run("link", "list"), h.stderr.String())

	require.Contains(t, h.stdout.String(), destination)
	require.NotContains(t, h.stdout.String(), "…")
}

func TestListPrintsAlignedColumnsUnderALocalisedHeaderOnATTY(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald,
		listedLink("sommerfest", "https://example.org/sommerfest", "active"),
		listedLink("alt", "https://example.org/alt", "disabled"),
	)
	h.stdoutTTY = true
	h.termWidth = 120

	require.Equal(t, 0, h.run("link", "list"), h.stderr.String())

	require.Equal(t,
		"Short URL                            Destination                     State\n"+
			"https://go.kurze-url.app/sommerfest  https://example.org/sommerfest  active\n"+
			"https://go.kurze-url.app/alt         https://example.org/alt         disabled\n",
		h.stdout.String())
}

func TestListTruncatesOnATTY(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald, listedLink("lang", "https://example.org/"+strings.Repeat("x", 100), "active"))
	h.stdoutTTY = true
	h.termWidth = 60

	require.Equal(t, 0, h.run("link", "list"), h.stderr.String())

	lines := strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n")
	require.Len(t, lines, 2, "a header and one row")
	for _, line := range lines {
		require.LessOrEqual(t, utf8.RuneCountInString(line), 60, line)
	}
	require.Contains(t, lines[1], "…")
	// The short URL and the state are the columns a person acts on; only the
	// destination gives way.
	require.Contains(t, lines[1], "https://go.kurze-url.app/lang")
	require.True(t, strings.HasSuffix(lines[1], "active"), lines[1])
}

func TestListLocalisesState(t *testing.T) {
	h := linkHarness(t, "language: de\nteam: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald,
		listedLink("a", "https://example.org/a", "active"),
		listedLink("b", "https://example.org/b", "disabled"),
		listedLink("c", "https://example.org/c", "expired"),
		listedLink("d", "https://example.org/d", "flagged"),
	)

	require.Equal(t, 0, h.run("link", "list"), h.stderr.String())

	var states []string
	for _, line := range strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n") {
		cells := strings.Split(line, "\t")
		states = append(states, cells[len(cells)-1])
	}
	require.Equal(t, []string{"aktiv", "deaktiviert", "abgelaufen", "gesperrt"}, states)
}

func TestListLocalisesTheHeaderInGerman(t *testing.T) {
	h := linkHarness(t, "language: de\nteam: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald, listedLink("a", "https://example.org/a", "active"))
	h.stdoutTTY = true

	require.Equal(t, 0, h.run("link", "list"), h.stderr.String())

	header := strings.SplitN(h.stdout.String(), "\n", 2)[0]
	require.Equal(t, []string{"Kurz-URL", "Ziel", "Status"}, strings.Fields(header))
}

// A state a newer API added shows up as the API wrote it, so the list stays
// truthful before the CLI learns the word.
func TestListShowsAnUnknownStateAsItIs(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinks(teamGruenwald, listedLink("a", "https://example.org/a", "archived"))

	require.Equal(t, 0, h.run("link", "list"), h.stderr.String())

	require.Equal(t, "https://go.kurze-url.app/a\thttps://example.org/a\tarchived\n", h.stdout.String())
}

// The configured folder is a default for creating. As a silent filter it would
// make links vanish from the list with nothing saying why.
func TestListIgnoresTheConfiguredFolder(t *testing.T) {
	h := linkHarness(t,
		"team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    folder: newsletter\n",
		teamGruenwald)
	// No folder list is registered: the fake API fails the test if the
	// command resolves a folder nobody asked for.
	h.serveLinks(teamGruenwald, listedLink("a", "https://example.org/a", "active"))

	require.Equal(t, 0, h.run("link", "list"), h.stderr.String())

	require.NotContains(t, h.listQuery(teamGruenwald), "folder_id")
}

func TestListRefusesAnUnknownFolder(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveFolders(teamGruenwald, testFolderNewsletter)

	code := h.run("link", "list", "--folder", "Plakate")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t,
		"kurze-url: There is no folder “Plakate” in the team “sv-gruenwald”. Available folders: newsletter.\n",
		h.stderr.String())
	require.Empty(t, h.api.callsTo(http.MethodGet, teamPath(teamGruenwald, "links")))
}

func TestListWithNothingSaysSoOnStderr(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprintf("tty=%v", tty), func(t *testing.T) {
			h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
			h.serveLinks(teamGruenwald)
			h.stdoutTTY = tty

			code := h.run("link", "list")

			require.Equal(t, 0, code, h.stderr.String())
			require.Empty(t, h.stdout.String(), "not even a header")
			require.Equal(t, "No links found.\n", h.stderr.String())
		})
	}
}

func TestListHonoursTheTeamFlag(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald, teamMuster)
	h.serveLinks(teamMuster, listedLink("a", "https://example.org/a", "active"))

	require.Equal(t, 0, h.run("link", "list", "--team", "tc-muster"), h.stderr.String())

	require.Contains(t, h.stdout.String(), "https://go.kurze-url.app/a")
	require.Empty(t, h.api.callsTo(http.MethodGet, teamPath(teamGruenwald, "links")))
}

func TestListRefusesAnArgument(t *testing.T) {
	h := newHarness(t)

	code := h.run("link", "list", "extra")

	require.Equal(t, 2, code)
	require.Contains(t, h.stderr.String(), "Run 'kurze-url link list --help' for usage.")
}
