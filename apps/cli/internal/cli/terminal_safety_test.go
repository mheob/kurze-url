package cli

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
)

// A name a team's admin chose can be anything: the API checks the length of a
// folder's or a team's name and nothing else. This one is an OSC 52 clipboard
// write, which a terminal obeys without showing anything, so a name that
// reaches the screen raw can change what the person pastes next.
const (
	hostileName = "evil\x1b]52;c;SGVsbG8=\x07"
	// hostileCleaned is what the person sees instead: the same text, with
	// every control character shown as the space it is replaced by.
	hostileCleaned = "evil ]52;c;SGVsbG8= "
)

// requireTamed fails when text still holds a control character a terminal
// acts on.
func requireTamed(t *testing.T, text string) {
	t.Helper()
	require.NotContains(t, text, "\x1b", "an escape sequence reached the terminal")
	require.NotContains(t, text, "\x07", "a bell character reached the terminal")
}

// hostileLink is a link whose short URL, as the API would send it, carries
// the control characters, on a host the lookup tests know.
func hostileLink(id int, team apiclient.Membership, host string) apiclient.Link {
	l := lookupLink(id, team, host, "sommerfest")
	l.ShortURL = "https://" + host + "/" + hostileName
	return l
}

func TestAnUnknownFolderListsNamesWithoutControlCharacters(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveFolders(teamGruenwald, apiclient.Folder{ID: "f1", Name: hostileName})

	code := h.run("link", "create", testDestination, "--folder", "Sommerfest")

	require.Equal(t, 1, code)
	requireTamed(t, h.stderr.String())
	require.Equal(t,
		"kurze-url: There is no folder “Sommerfest” in the team “sv-gruenwald”. "+
			"Available folders: "+hostileCleaned+".\n",
		h.stderr.String())
}

// The team's slug is in the same sentence, and so is a hostname in the domain
// refusals. A slug is held to a pattern by the API, but this code does not
// rest on that.
func TestRefusalsNameTheTeamWithoutControlCharacters(t *testing.T) {
	hostileSlug := apiclient.Membership{TeamID: teamGruenwald.TeamID, Name: "SV", Slug: "evil\x1b[0mslug", Role: "owner"}
	h := linkHarness(t, "", hostileSlug)
	h.serveFolders(hostileSlug)
	h.serveDomains(hostileSlug)

	code := h.run("link", "create", testDestination, "--team", hostileSlug.Slug, "--folder", "newsletter")
	require.Equal(t, 1, code)
	requireTamed(t, h.stderr.String())
	require.Contains(t, h.stderr.String(), "in the team “evil [0mslug”")

	h.stderr.Reset()
	code = h.run("link", "create", testDestination, "--team", hostileSlug.Slug, "--domain", "links.example.org")
	require.Equal(t, 1, code)
	requireTamed(t, h.stderr.String())
	require.Contains(t, h.stderr.String(), "The team “evil [0mslug” has no domain")
}

func TestTheLoginPromptListsTeamsWithoutControlCharacters(t *testing.T) {
	h := newHarness(t)
	h.serveMe(
		apiclient.Membership{TeamID: teamGruenwald.TeamID, Name: hostileName, Slug: "sv-gruenwald", Role: "owner"},
		teamMuster,
	)
	h.stdinTTY = true
	h.setStdin("2\n")

	code := h.run("login")

	require.Equal(t, 0, code, h.stderr.String())
	requireTamed(t, h.stderr.String())
	require.Contains(t, h.stderr.String(), "1) sv-gruenwald ("+hostileCleaned+")\n")
}

func TestAnEmailWithControlCharactersIsShownWithoutThem(t *testing.T) {
	serve := func(h *harness) {
		h.api.respond(http.MethodGet, apiclient.PathMe, http.StatusOK, apiclient.Me{
			UserID:      "00000000-0000-4000-8000-0000000000aa",
			Email:       hostileName + "@example.org",
			Memberships: []apiclient.Membership{teamGruenwald},
		})
	}

	t.Run("whoami", func(t *testing.T) {
		h := newHarness(t)
		h.signIn()
		serve(h)

		code := h.run("whoami")

		require.Equal(t, 0, code, h.stderr.String())
		requireTamed(t, h.stdout.String())
		require.Equal(t, hostileCleaned+"@example.org\nNo default team\n", h.stdout.String())
	})

	t.Run("login", func(t *testing.T) {
		h := newHarness(t)
		serve(h)

		code := h.run("login")

		require.Equal(t, 0, code, h.stderr.String())
		requireTamed(t, h.stderr.String())
		require.Contains(t, h.stderr.String(), "Signed in as "+hostileCleaned+"@example.org")
	})
}

func TestAmbiguousCandidatesAreListedWithoutControlCharacters(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald,
		hostileLink(0xa1, teamGruenwald, "go.kurze-url.app"),
		lookupCustom)

	code := h.run("link", "stats", "sommerfest")

	require.Equal(t, 1, code)
	requireTamed(t, h.stderr.String())
	require.Contains(t, h.stderr.String(), "\nhttps://go.kurze-url.app/"+hostileCleaned+"\nhttps://links.sv-gruenwald.de/sommerfest\n")
}

func TestACreatedLinksShortURLIsPrintedWithoutControlCharacters(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	link := hostileLink(0xc1, teamGruenwald, "go.kurze-url.app")
	h.api.respond(http.MethodPost, teamPath(teamGruenwald, "links"), http.StatusCreated, link)

	code := h.run("link", "create", testDestination)

	require.Equal(t, 0, code, h.stderr.String())
	requireTamed(t, h.stdout.String())
	require.Equal(t, "https://go.kurze-url.app/"+hostileCleaned+"\n", h.stdout.String())
}

func TestTheStatsHeadingIsPrintedWithoutControlCharacters(t *testing.T) {
	link := hostileLink(0xa1, teamGruenwald, "go.kurze-url.app")
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald, link)
	h.api.respond(http.MethodGet, statsPath(link), http.StatusOK, windowsStats(thirtyDays()))

	code := h.run("link", "stats", "sommerfest")

	require.Equal(t, 0, code, h.stderr.String())
	requireTamed(t, h.stdout.String())
	require.Contains(t, h.stdout.String(), "https://go.kurze-url.app/"+hostileCleaned+"\n")
}

// A failure the API words itself is shown as it came, and what it came with
// is a server's text like any other.
func TestAnAPIFailureTextIsShownWithoutControlCharacters(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.api.problem(http.MethodGet, teamPath(teamGruenwald, "links"), http.StatusBadRequest,
		"bad query "+hostileName)

	code := h.run("link", "list")

	require.Equal(t, 1, code)
	requireTamed(t, h.stderr.String())
	require.Contains(t, h.stderr.String(), "bad query "+hostileCleaned)
}
