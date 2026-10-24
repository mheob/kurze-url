package cli

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
)

// The destination every create test shortens, and the short URL the fake API
// answers with. The short URL is the only thing a successful run may print.
const (
	testDestination = "https://example.org/sommerfest"
	testShortURL    = "https://go.kurze-url.app/abc12345"
)

// teamPath is the path of a team-scoped collection, as the router sees it.
func teamPath(team apiclient.Membership, collection string) string {
	return "/v1/teams/" + team.TeamID + "/" + collection
}

// linkHarness is a harness whose person is signed in, has this config file
// (none when empty) and belongs to these teams.
func linkHarness(t *testing.T, config string, teams ...apiclient.Membership) *harness {
	t.Helper()
	h := newHarness(t)
	h.signIn()
	if config != "" {
		h.writeConfig(config)
	}
	h.serveMe(teams...)
	return h
}

// serveFolders makes a team's folder list answer with these folders.
func (h *harness) serveFolders(team apiclient.Membership, folders ...apiclient.Folder) {
	h.t.Helper()
	if folders == nil {
		folders = []apiclient.Folder{}
	}
	h.api.respond(http.MethodGet, teamPath(team, "folders"), http.StatusOK, apiclient.Page[apiclient.Folder]{
		Items: folders, Page: 1, PerPage: 100, TotalCount: len(folders),
	})
}

// serveDomains makes a team's domain list answer with these domains.
func (h *harness) serveDomains(team apiclient.Membership, domains ...apiclient.Domain) {
	h.t.Helper()
	if domains == nil {
		domains = []apiclient.Domain{}
	}
	h.api.respond(http.MethodGet, teamPath(team, "domains"), http.StatusOK, apiclient.Page[apiclient.Domain]{
		Items: domains, Page: 1, PerPage: 100, TotalCount: len(domains),
	})
}

// serveCreate makes a team's link creation answer with a link whose short URL
// is testShortURL.
func (h *harness) serveCreate(team apiclient.Membership) {
	h.t.Helper()
	h.api.respond(http.MethodPost, teamPath(team, "links"), http.StatusCreated, apiclient.Link{
		ID:             "00000000-0000-4000-8000-0000000000c1",
		TeamID:         team.TeamID,
		Hostname:       "go.kurze-url.app",
		Slug:           "abc12345",
		ShortURL:       testShortURL,
		DestinationURL: testDestination,
		State:          "active",
	})
}

// creations returns the bodies of the link creations a team received, decoded.
func (h *harness) creations(team apiclient.Membership) []map[string]any {
	h.t.Helper()
	var bodies []map[string]any
	for _, c := range h.api.callsTo(http.MethodPost, teamPath(team, "links")) {
		var body map[string]any
		require.NoError(h.t, json.Unmarshal(c.Body, &body))
		bodies = append(bodies, body)
	}
	return bodies
}

var (
	testFolderNewsletter = apiclient.Folder{ID: "00000000-0000-4000-8000-0000000000f1", Name: "newsletter"}
	testFolderPlakate    = apiclient.Folder{ID: "00000000-0000-4000-8000-0000000000f2", Name: "Plakate"}

	testDomainVerified = apiclient.Domain{
		ID:                 "00000000-0000-4000-8000-0000000000d1",
		Hostname:           "links.sv-gruenwald.de",
		VerificationStatus: "verified",
	}
	testDomainPending = apiclient.Domain{
		ID:                 "00000000-0000-4000-8000-0000000000d2",
		Hostname:           "neu.sv-gruenwald.de",
		VerificationStatus: "pending",
	}
)

// A pipe such as `| pbcopy` must receive the short URL and one newline, and
// nothing else, even when a warning fires in the same run. The stale section
// makes `me` warn, and the warning must land on stderr.
func TestCreatePrintsOnlyTheShortURL(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\nteams:\n  old-team:\n    folder: newsletter\n", teamGruenwald)
	h.serveCreate(teamGruenwald)

	code := h.run("link", "create", testDestination)

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, testShortURL+"\n", h.stdout.String())
	require.Equal(t,
		"Ignoring the settings for “old-team” in your config file: you are not a member of that team.\n",
		h.stderr.String())
}

// Everything but the destination is left out of the request when it was not
// asked for, so the API applies its own defaults.
func TestCreateSendsOnlyTheDestinationByDefault(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveCreate(teamGruenwald)

	require.Equal(t, 0, h.run("link", "create", testDestination), h.stderr.String())

	require.Equal(t, []map[string]any{{"destination_url": testDestination}}, h.creations(teamGruenwald))
}

func TestCreateSendsSlugAndFolderID(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveFolders(teamGruenwald, testFolderPlakate, testFolderNewsletter)
	h.serveCreate(teamGruenwald)

	code := h.run("link", "create", testDestination, "--slug", "SommerFest", "--folder", "NEWSLETTER")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, []map[string]any{{
		"destination_url": testDestination,
		// The slug travels as typed: the API folds case, the CLI does not
		// second-guess it.
		"slug":      "SommerFest",
		"folder_id": testFolderNewsletter.ID,
	}}, h.creations(teamGruenwald))
	require.Equal(t, testShortURL+"\n", h.stdout.String())
}

func TestCreateUsesTheTeamsConfiguredFolderAndDomain(t *testing.T) {
	h := linkHarness(t,
		"team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    folder: newsletter\n    domain: links.sv-gruenwald.de\n",
		teamGruenwald)
	h.serveFolders(teamGruenwald, testFolderNewsletter)
	h.serveDomains(teamGruenwald, testDomainPending, testDomainVerified)
	h.serveCreate(teamGruenwald)

	code := h.run("link", "create", testDestination)

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, []map[string]any{{
		"destination_url": testDestination,
		"folder_id":       testFolderNewsletter.ID,
		"domain_id":       testDomainVerified.ID,
	}}, h.creations(teamGruenwald))
}

// A flag beats the team's configured value, so a person can put one link
// elsewhere without editing the file.
func TestCreateFlagsBeatTheConfiguredFolderAndDomain(t *testing.T) {
	h := linkHarness(t,
		"team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    folder: newsletter\n    domain: links.sv-gruenwald.de\n",
		teamGruenwald)
	h.serveFolders(teamGruenwald, testFolderNewsletter, testFolderPlakate)
	h.serveDomains(teamGruenwald, testDomainVerified)
	h.serveCreate(teamGruenwald)

	code := h.run("link", "create", testDestination, "--folder", "plakate", "--domain", "go.kurze-url.app")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, []map[string]any{{
		"destination_url": testDestination,
		"folder_id":       testFolderPlakate.ID,
	}}, h.creations(teamGruenwald), "the shared host is the API's default and is not sent")
}

// Hostnames do not care about case, and a person types them however they
// copied them. The config file insists on lower case, the flag does not.
func TestCreateMatchesTheDomainIgnoringCase(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveDomains(teamGruenwald, testDomainVerified)
	h.serveCreate(teamGruenwald)

	code := h.run("link", "create", testDestination, "--domain", "LINKS.SV-GRUENWALD.DE")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, testDomainVerified.ID, h.creations(teamGruenwald)[0]["domain_id"])
}

func TestCreateOnTheSharedHostSendsNoDomainID(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	// No domain list is registered: the fake API fails the test if the
	// command asks for one, which it must not for the shared host.
	h.serveCreate(teamGruenwald)

	code := h.run("link", "create", testDestination, "--domain", "GO.KURZE-URL.APP")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, []map[string]any{{"destination_url": testDestination}}, h.creations(teamGruenwald))
	require.Empty(t, h.api.callsTo(http.MethodGet, teamPath(teamGruenwald, "domains")))
}

func TestCreateOnAnotherInstancesSharedHost(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.env["KURZE_URL_SHARED_HOST"] = "short.invalid"
	h.serveCreate(teamGruenwald)

	require.Equal(t, 0, h.run("link", "create", testDestination, "--domain", "short.invalid"), h.stderr.String())

	require.Equal(t, []map[string]any{{"destination_url": testDestination}}, h.creations(teamGruenwald))
}

func TestCreateRefusesAnUnverifiedDomain(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveDomains(teamGruenwald, testDomainVerified, testDomainPending)

	code := h.run("link", "create", testDestination, "--domain", "neu.sv-gruenwald.de")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t,
		"kurze-url: The domain “neu.sv-gruenwald.de” is not verified yet, so links cannot use it. "+
			"Finish verifying it in the web app first.\n",
		h.stderr.String())
	require.Empty(t, h.creations(teamGruenwald), "no link may be created on a domain that is not verified")
}

func TestCreateRefusesAnUnknownDomain(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveDomains(teamGruenwald, testDomainVerified)

	code := h.run("link", "create", testDestination, "--domain", "links.tc-muster.de")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t,
		"kurze-url: The team “sv-gruenwald” has no domain “links.tc-muster.de”. "+
			"Use one of its verified domains, or the shared domain go.kurze-url.app.\n",
		h.stderr.String())
	require.Empty(t, h.creations(teamGruenwald))
}

func TestCreateRefusesAnUnknownFolder(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveFolders(teamGruenwald, testFolderNewsletter, testFolderPlakate)

	code := h.run("link", "create", testDestination, "--folder", "Sommerfest")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t,
		"kurze-url: There is no folder “Sommerfest” in the team “sv-gruenwald”. "+
			"Available folders: newsletter, Plakate.\n",
		h.stderr.String())
	// A folder is never created on the way, and no link is made without it.
	require.Empty(t, h.creations(teamGruenwald))
	for _, c := range h.api.requests() {
		require.Equal(t, http.MethodGet, c.Method, "no write of any kind: %s", c.Path)
	}
}

// A team with no folders at all still gets a sentence that reads.
func TestCreateRefusesAFolderInATeamWithNone(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveFolders(teamGruenwald)

	code := h.run("link", "create", testDestination, "--folder", "newsletter")

	require.Equal(t, 1, code)
	require.Equal(t,
		"kurze-url: There is no folder “newsletter” in the team “sv-gruenwald”. Available folders: none.\n",
		h.stderr.String())
}

func TestCreateRefusalsSpeakGerman(t *testing.T) {
	h := linkHarness(t, "language: de\nteam: sv-gruenwald\n", teamGruenwald)
	h.serveFolders(teamGruenwald)

	code := h.run("link", "create", testDestination, "--folder", "newsletter")

	require.Equal(t, 1, code)
	require.Equal(t,
		"kurze-url: Im Team „sv-gruenwald“ gibt es keinen Ordner „newsletter“. Vorhandene Ordner: keine.\n",
		h.stderr.String())
}

// The destination is checked before anything is asked of the API, the memberships
// included: a typo should not cost a round trip, or a login.
func TestCreateRejectsNonHTTPSLocally(t *testing.T) {
	for _, destination := range []string{"example.org", "http://example.org", "https://", "ftp://example.org", "https://:8443/x"} {
		t.Run(destination, func(t *testing.T) {
			h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)

			code := h.run("link", "create", destination)

			require.Equal(t, 1, code)
			require.Empty(t, h.stdout.String())
			require.Equal(t, "kurze-url: The destination must be a complete https:// address.\n", h.stderr.String())
			require.Empty(t, h.api.requests(), "no request, not even /v1/me")
		})
	}
}

func TestCreateAcceptsAnUppercaseScheme(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveCreate(teamGruenwald)

	require.Equal(t, 0, h.run("link", "create", "HTTPS://example.org/x"), h.stderr.String())

	// Sent as typed. The API judges the destination again, and it is the
	// authority on what it keeps.
	require.Equal(t, []map[string]any{{"destination_url": "HTTPS://example.org/x"}}, h.creations(teamGruenwald))
}

func TestCreateExplainsATakenSlug(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.api.problem(http.MethodPost, teamPath(teamGruenwald, "links"), http.StatusConflict,
		"that slug is already taken on this domain")

	code := h.run("link", "create", testDestination, "--slug", "sommerfest")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t, "kurze-url: That slug is already taken on this domain.\n", h.stderr.String())
}

func TestCreateExplainsATakenSlugInGerman(t *testing.T) {
	h := linkHarness(t, "language: de\nteam: sv-gruenwald\n", teamGruenwald)
	h.api.problem(http.MethodPost, teamPath(teamGruenwald, "links"), http.StatusConflict, "taken")

	require.Equal(t, 1, h.run("link", "create", testDestination, "--slug", "sommerfest"))

	require.Equal(t, "kurze-url: Dieser Slug ist auf dieser Domain schon vergeben.\n", h.stderr.String())
}

// Only a 409 means "taken". The API's other answers keep the generic wording,
// and a 422 on the destination keeps its own.
func TestCreateKeepsTheGenericWordingForOtherFailures(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.api.problem(http.MethodPost, teamPath(teamGruenwald, "links"), http.StatusUnprocessableEntity, "bad",
		apiclient.ProblemDetail{Location: "body.destination_url", Message: "refused"})

	require.Equal(t, 1, h.run("link", "create", testDestination))

	require.Equal(t, "kurze-url: The destination must be a complete https:// address.\n", h.stderr.String())
}

func TestCreateUsesTheDefaultTeam(t *testing.T) {
	h := linkHarness(t, "team: tc-muster\n", teamGruenwald, teamMuster)
	h.serveCreate(teamMuster)

	code := h.run("link", "create", testDestination)

	require.Equal(t, 0, code, h.stderr.String())
	require.Len(t, h.creations(teamMuster), 1)
	require.Empty(t, h.creations(teamGruenwald))
}

func TestCreateHonoursTeamFlag(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald, teamMuster)
	h.serveCreate(teamMuster)

	code := h.run("link", "create", testDestination, "--team", "tc-muster")

	require.Equal(t, 0, code, h.stderr.String())
	require.Len(t, h.creations(teamMuster), 1)
	require.Empty(t, h.creations(teamGruenwald))
}

// A slug is lower-case by rule, so --team SV-Gruenwald is the team
// sv-gruenwald and not a stranger.
func TestCreateReadsTheTeamFlagIgnoringCase(t *testing.T) {
	h := linkHarness(t, "team: tc-muster\n", teamGruenwald, teamMuster)
	h.serveCreate(teamGruenwald)

	code := h.run("link", "create", testDestination, "--team", "SV-Gruenwald")

	require.Equal(t, 0, code, h.stderr.String())
	require.Len(t, h.creations(teamGruenwald), 1)
	require.Empty(t, h.creations(teamMuster))
}

// The folder and the domain are looked up in the team the link goes to, not
// in the default team.
func TestCreateLooksFoldersUpInTheChosenTeam(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald, teamMuster)
	h.serveFolders(teamMuster, testFolderPlakate)
	h.serveCreate(teamMuster)

	code := h.run("link", "create", testDestination, "--team", "tc-muster", "--folder", "plakate")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, testFolderPlakate.ID, h.creations(teamMuster)[0]["folder_id"])
}

// The other team's configured folder must not follow the link to this team.
func TestCreateReadsTheConfiguredFolderOfTheChosenTeam(t *testing.T) {
	h := linkHarness(t,
		"team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    folder: newsletter\n",
		teamGruenwald, teamMuster)
	h.serveCreate(teamMuster)

	code := h.run("link", "create", testDestination, "--team", "tc-muster")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, []map[string]any{{"destination_url": testDestination}}, h.creations(teamMuster))
}

func TestCreateWithoutATeamSaysSo(t *testing.T) {
	h := linkHarness(t, "", teamGruenwald)

	code := h.run("link", "create", testDestination)

	require.Equal(t, 1, code)
	require.Contains(t, h.stderr.String(), "No team given.")
	require.Empty(t, h.stdout.String())
}

func TestCreateNeedsExactlyOneURL(t *testing.T) {
	for _, args := range [][]string{{"link", "create"}, {"link", "create", "https://a.example", "https://b.example"}} {
		h := newHarness(t)

		code := h.run(args...)

		require.Equal(t, 2, code, args)
		require.Contains(t, h.stderr.String(), "Run 'kurze-url link create --help' for usage.")
		require.Empty(t, h.api.requests())
	}
}

func TestLinkWithoutSubcommandIsAUsageError(t *testing.T) {
	h := newHarness(t)

	code := h.run("link")

	require.Equal(t, 2, code)
	require.Empty(t, h.stdout.String())
	require.Contains(t, h.stderr.String(), "create")
	require.Contains(t, h.stderr.String(), "list")
	require.Contains(t, h.stderr.String(), "Run 'kurze-url link --help' for usage.")
	require.Empty(t, h.api.requests())
}
