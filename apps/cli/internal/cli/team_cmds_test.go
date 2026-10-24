package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTeamListMarksTheDefault(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("team: sv-gruenwald\n")
	h.serveMe(teamGruenwald, teamMuster)

	code := h.run("team", "list")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t,
		"*\tsv-gruenwald\tSV Grünwald\tOwner\n"+
			"\ttc-muster\tTC Muster\tViewer\n",
		h.stdout.String())
	require.Empty(t, h.stderr.String())
}

func TestTeamListMarksNothingWithoutADefault(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.serveMe(teamGruenwald, teamMuster)

	require.Equal(t, 0, h.run("team", "list"), h.stderr.String())

	require.Equal(t,
		"\tsv-gruenwald\tSV Grünwald\tOwner\n"+
			"\ttc-muster\tTC Muster\tViewer\n",
		h.stdout.String())
}

func TestTeamListOnATTYPrintsATable(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("team: tc-muster\n")
	h.serveMe(teamGruenwald, teamMuster)
	h.stdoutTTY = true

	code := h.run("team", "list")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t,
		"   Slug          Name         Role\n"+
			"   sv-gruenwald  SV Grünwald  Owner\n"+
			"*  tc-muster     TC Muster    Viewer\n",
		h.stdout.String())
}

func TestTeamListShrinksTheNameToTheTerminalWidth(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	long := teamGruenwald
	long.Name = strings.Repeat("Sehr langer Vereinsname ", 6)
	h.serveMe(long)
	h.stdoutTTY = true
	h.termWidth = 50

	require.Equal(t, 0, h.run("team", "list"), h.stderr.String())

	for _, line := range strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n") {
		require.LessOrEqual(t, len([]rune(line)), 50, line)
	}
	require.Contains(t, h.stdout.String(), "…")
}

func TestTeamListNeverShrinksAPipe(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	long := teamGruenwald
	long.Name = strings.Repeat("Sehr langer Vereinsname ", 6)
	h.serveMe(long)
	h.termWidth = 30

	require.Equal(t, 0, h.run("team", "list"), h.stderr.String())

	require.Contains(t, h.stdout.String(), long.Name)
	require.NotContains(t, h.stdout.String(), "…")
}

func TestTeamListLocalisesTheRolesAndTheHeader(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("language: de\n")
	editor := teamMuster
	editor.Slug, editor.Role = "tc-editor", "editor"
	admin := teamMuster
	admin.Slug, admin.Role = "tc-admin", "admin"
	h.serveMe(teamGruenwald, editor, teamMuster, admin)
	h.stdoutTTY = true

	require.Equal(t, 0, h.run("team", "list"), h.stderr.String())

	out := h.stdout.String()
	require.Contains(t, out, "Rolle")
	for _, want := range []string{"Eigentum", "Bearbeitung", "Leserecht", "Admin"} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "Owner")
}

func TestTeamListShowsAnUnknownRoleAsItIs(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	odd := teamGruenwald
	odd.Role = "guest"
	h.serveMe(odd)

	require.Equal(t, 0, h.run("team", "list"), h.stderr.String())

	require.Equal(t, "\tsv-gruenwald\tSV Grünwald\tguest\n", h.stdout.String())
}

func TestTeamListWithoutTeamsSaysSo(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.serveMe()
	h.stdoutTTY = true

	code := h.run("team", "list")

	require.Equal(t, 0, code, h.stderr.String())
	require.Empty(t, h.stdout.String())
	require.Equal(t, "You are not a member of any team yet.\n", h.stderr.String())
}

func TestTeamWithoutSubcommandIsAUsageError(t *testing.T) {
	h := newHarness(t)

	code := h.run("team")

	require.Equal(t, 2, code)
	require.Empty(t, h.stdout.String())
	require.Contains(t, h.stderr.String(), "list")
	require.Contains(t, h.stderr.String(), "Run 'kurze-url team --help' for usage.")
	require.Empty(t, h.api.requests())
}

func TestTeamWithAnUnknownSubcommandIsAUsageError(t *testing.T) {
	h := newHarness(t)

	code := h.run("team", "nope")

	require.Equal(t, 2, code)
	require.Contains(t, h.stderr.String(), "unknown command")
}

func TestTeamListRefusesAnArgument(t *testing.T) {
	h := newHarness(t)

	code := h.run("team", "list", "extra")

	require.Equal(t, 2, code)
	require.Contains(t, h.stderr.String(), "Run 'kurze-url team list --help' for usage.")
}

// A section for a team the person has left is a warning and never an error,
// and the warning must not leak into what a script reads.
func TestStaleTeamSectionWarnsOnStderrOnly(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("teams:\n  old-team:\n    folder: newsletter\n")
	h.serveMe(teamGruenwald)

	code := h.run("team", "list")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, "\tsv-gruenwald\tSV Grünwald\tOwner\n", h.stdout.String())
	require.Equal(t,
		"Ignoring the settings for “old-team” in your config file: you are not a member of that team.\n",
		h.stderr.String())
}

func TestStaleTeamSectionWarnsInGerman(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("language: de\nteams:\n  old-team:\n    folder: newsletter\n")
	h.serveMe(teamGruenwald)

	require.Equal(t, 0, h.run("team", "list"), h.stderr.String())

	require.Contains(t, h.stderr.String(), "„old-team“")
}

func TestASectionForAMembershipDoesNotWarn(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	h.writeConfig("teams:\n  sv-gruenwald:\n    folder: newsletter\n")
	h.serveMe(teamGruenwald)

	require.Equal(t, 0, h.run("team", "list"), h.stderr.String())

	require.Empty(t, h.stderr.String())
}
