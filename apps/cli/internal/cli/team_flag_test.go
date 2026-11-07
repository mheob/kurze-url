package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An empty --team used to mean "global" on config and "the default team" on
// the link commands, so `config set language de --team ""` wrote the global
// section without a word. A flag that was typed and names nothing is a
// mistake in how the command was invoked, on every command that has it.
func TestAnEmptyTeamFlagIsAUsageError(t *testing.T) {
	commands := [][]string{
		{"config", "get", "language"},
		{"config", "set", "language", "de"},
		{"config", "unset", "language"},
		{"link", "create", "https://example.org/"},
		{"link", "list"},
		{"link", "qr", "sommerfest"},
		{"link", "stats", "sommerfest"},
	}
	for _, command := range commands {
		for _, value := range []string{"", "   "} {
			name := strings.Join(command, " ") + " --team " + `"` + value + `"`
			t.Run(name, func(t *testing.T) {
				const config = "language: en\nteam: sv-gruenwald\n"
				h := linkHarness(t, config, teamGruenwald)

				code := h.run(append(append([]string{}, command...), "--team", value)...)

				require.Equal(t, 2, code)
				require.Empty(t, h.stdout.String())
				require.Equal(t,
					"kurze-url: --team needs a team slug.\n"+
						"Run 'kurze-url "+strings.Join(command[:2], " ")+" --help' for usage.\n",
					h.stderr.String())
				require.Equal(t, config, h.config())
				require.Empty(t, h.api.requests())
			})
		}
	}
}

func TestTheTeamFlagMessageIsWordedInTheConfiguredLanguage(t *testing.T) {
	h := linkHarness(t, "language: de\n", teamGruenwald)

	code := h.run("config", "set", "language", "en", "--team", "")

	require.Equal(t, 2, code)
	require.Contains(t, h.stderr.String(), "kurze-url: --team braucht einen Team-Slug.\n")
	require.Equal(t, "language: de\n", h.config())
}
