package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// resetOutput clears what the harness collected, for a test that runs two
// commands and judges the second.
func (h *harness) resetOutput() {
	h.stdout.Reset()
	h.stderr.Reset()
}

func TestConfigSetThenGet(t *testing.T) {
	h := newHarness(t)

	code := h.run("config", "set", "qr.fg", "#1A3A6E", "--team", "sv-gruenwald")

	require.Equal(t, 0, code, h.stderr.String())
	require.Empty(t, h.stdout.String(), "a message is not a result")
	require.Equal(t, "Saved to "+h.configPath+"\n", h.stderr.String())
	// The colour is stored the way the file format wants it: no hash, lower
	// case, quoted.
	require.Equal(t, "teams:\n  sv-gruenwald:\n    qr:\n      fg: '1a3a6e'\n", h.config())

	h.resetOutput()
	code = h.run("config", "get", "qr.fg", "--team", "sv-gruenwald")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, "1a3a6e\n", h.stdout.String())
	require.Empty(t, h.stderr.String())
}

func TestConfigCommandsNeverCallTheServices(t *testing.T) {
	h := newHarness(t)
	// Signed out, and the team is not one anybody has been asked about:
	// membership changes, so the file takes the section anyway.
	steps := [][]string{
		{"config", "set", "folder", "newsletter", "--team", "tc-muster"},
		{"config", "get"},
		{"config", "get", "folder", "--team", "tc-muster"},
		{"config", "unset", "folder", "--team", "tc-muster"},
	}

	for _, args := range steps {
		require.Equal(t, 0, h.run(args...), "%v: %s", args, h.stderr.String())
	}

	require.Empty(t, h.api.requests())
	require.Empty(t, h.auth.calls)
}

func TestConfigGetListsSources(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(`qr:
  format: png
teams:
  sv-gruenwald:
    qr:
      size: 1024
`)

	code := h.run("config", "get", "--team", "sv-gruenwald")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t,
		"language\ten\tsystem\n"+
			"qr.format\tpng\tglobal\n"+
			"qr.size\t1024\tteam sv-gruenwald\n",
		h.stdout.String())
	require.Empty(t, h.stderr.String())
}

func TestConfigGetPrefersTheTeamsValueOverTheGlobalOne(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(`qr:
  fg: '000000'
  bg: 'ffffff'
teams:
  sv-gruenwald:
    qr:
      fg: '1a3a6e'
`)

	require.Equal(t, 0, h.run("config", "get", "--team", "sv-gruenwald"), h.stderr.String())

	require.Equal(t,
		"language\ten\tsystem\n"+
			"qr.fg\t1a3a6e\tteam sv-gruenwald\n"+
			"qr.bg\tffffff\tglobal\n",
		h.stdout.String())

	h.resetOutput()
	require.Equal(t, 0, h.run("config", "get", "qr.fg", "--team", "sv-gruenwald"), h.stderr.String())
	require.Equal(t, "1a3a6e\n", h.stdout.String())

	// Another team has no section of its own and gets the global value.
	h.resetOutput()
	require.Equal(t, 0, h.run("config", "get", "qr.fg", "--team", "tc-muster"), h.stderr.String())
	require.Equal(t, "000000\n", h.stdout.String())
}

func TestConfigGetWithoutAKeyUsesTheConfiguredTeam(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(`team: sv-gruenwald
teams:
  sv-gruenwald:
    folder: newsletter
    domain: links.sv-gruenwald.de
  tc-muster:
    folder: jugend
`)

	require.Equal(t, 0, h.run("config", "get"), h.stderr.String())
	require.Equal(t,
		"language\ten\tsystem\n"+
			"team\tsv-gruenwald\tglobal\n"+
			"folder\tnewsletter\tteam sv-gruenwald\n"+
			"domain\tlinks.sv-gruenwald.de\tteam sv-gruenwald\n",
		h.stdout.String())

	// --team beats the configured one.
	h.resetOutput()
	require.Equal(t, 0, h.run("config", "get", "--team", "tc-muster"), h.stderr.String())
	require.Equal(t,
		"language\ten\tsystem\n"+
			"team\tsv-gruenwald\tglobal\n"+
			"folder\tjugend\tteam tc-muster\n",
		h.stdout.String())
}

func TestConfigGetWithoutATeamListsOnlyTheGlobalKeys(t *testing.T) {
	h := newHarness(t)
	// A team section exists, but nothing says which team is meant.
	h.writeConfig(`language: de
qr:
  size: 512
teams:
  sv-gruenwald:
    folder: newsletter
`)

	require.Equal(t, 0, h.run("config", "get"), h.stderr.String())

	require.Equal(t,
		"language\tde\tglobal\n"+
			"qr.size\t512\tglobal\n",
		h.stdout.String())
}

func TestConfigGetOfAnEmptyConfigStillShowsTheLanguage(t *testing.T) {
	h := newHarness(t)

	require.Equal(t, 0, h.run("config", "get"), h.stderr.String())

	require.Equal(t, "language\ten\tsystem\n", h.stdout.String())
	require.NoFileExists(t, h.configPath, "a read creates nothing")
}

func TestConfigGetTakesTheDetectedLanguageFromTheEnvironment(t *testing.T) {
	h := newHarness(t)
	h.env["LANG"] = "de_DE.UTF-8"

	require.Equal(t, 0, h.run("config", "get"), h.stderr.String())

	// The values are the same in every language, the source label is not.
	require.Equal(t, "language\tde\tSystem\n", h.stdout.String())

	h.resetOutput()
	require.Equal(t, 0, h.run("config", "get", "language"), h.stderr.String())
	require.Equal(t, "de\n", h.stdout.String())
}

func TestConfigGetPrintsATableOnATTY(t *testing.T) {
	h := newHarness(t)
	h.stdoutTTY = true
	h.writeConfig(`qr:
  format: png
teams:
  sv-gruenwald:
    qr:
      size: 1024
`)

	require.Equal(t, 0, h.run("config", "get", "--team", "sv-gruenwald"), h.stderr.String())

	require.Equal(t,
		"Setting    Value  Source\n"+
			"language   en     system\n"+
			"qr.format  png    global\n"+
			"qr.size    1024   team sv-gruenwald\n",
		h.stdout.String())
}

func TestConfigGetShrinksALongValueToTheTerminalWidth(t *testing.T) {
	h := newHarness(t)
	h.stdoutTTY = true
	h.termWidth = 40
	h.writeConfig("team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    folder: " +
		strings.Repeat("Sehr langer Ordnername ", 5) + "\n")

	require.Equal(t, 0, h.run("config", "get"), h.stderr.String())

	for _, line := range strings.Split(strings.TrimSuffix(h.stdout.String(), "\n"), "\n") {
		require.LessOrEqual(t, len([]rune(line)), 40, line)
	}
	require.Contains(t, h.stdout.String(), "…")

	// The one-value form is for scripts and is never shortened.
	h.resetOutput()
	require.Equal(t, 0, h.run("config", "get", "folder"), h.stderr.String())
	require.Equal(t, strings.TrimSpace(strings.Repeat("Sehr langer Ordnername ", 5))+"\n", h.stdout.String())
}

func TestConfigGetInGerman(t *testing.T) {
	h := newHarness(t)
	h.stdoutTTY = true
	h.writeConfig("language: de\nteams:\n  sv-gruenwald:\n    folder: newsletter\n")

	require.Equal(t, 0, h.run("config", "get", "--team", "sv-gruenwald"), h.stderr.String())

	require.Equal(t,
		"Einstellung  Wert        Quelle\n"+
			"language     de          global\n"+
			"folder       newsletter  Team sv-gruenwald\n",
		h.stdout.String())

	h.resetOutput()
	require.Equal(t, 1, h.run("config", "get", "qr.fg"))
	require.Equal(t, "kurze-url: „qr.fg“ ist nicht gesetzt.\n", h.stderr.String())
}

func TestConfigGetUnsetKeyExits1(t *testing.T) {
	h := newHarness(t)

	code := h.run("config", "get", "qr.fg")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t, "kurze-url: “qr.fg” is not set.\n", h.stderr.String())
}

func TestConfigGetKeyReadsTheEffectiveValueForTheConfiguredTeam(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    folder: newsletter\n")

	require.Equal(t, 0, h.run("config", "get", "folder"), h.stderr.String())
	require.Equal(t, "newsletter\n", h.stdout.String())

	// Without a team, folder has no meaning and is simply not set.
	h.resetOutput()
	h.writeConfig("teams:\n  sv-gruenwald:\n    folder: newsletter\n")
	require.Equal(t, 1, h.run("config", "get", "folder"))
	require.Equal(t, "kurze-url: “folder” is not set.\n", h.stderr.String())
}

func TestConfigGetUnknownKeyIsAUsageError(t *testing.T) {
	h := newHarness(t)

	code := h.run("config", "get", "qr.colour")

	require.Equal(t, 2, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t,
		"kurze-url: Unknown setting “qr.colour”.\n"+
			"Run 'kurze-url config get --help' for usage.\n",
		h.stderr.String())
}

func TestConfigSetUnknownKeyIsAUsageError(t *testing.T) {
	h := newHarness(t)

	code := h.run("config", "set", "colour", "red")

	require.Equal(t, 2, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t,
		"kurze-url: Unknown setting “colour”.\n"+
			"Run 'kurze-url config set --help' for usage.\n",
		h.stderr.String())
	require.NoFileExists(t, h.configPath)
}

func TestConfigUnsetUnknownKeyIsAUsageError(t *testing.T) {
	h := newHarness(t)

	require.Equal(t, 2, h.run("config", "unset", "colour"))
	require.Contains(t, h.stderr.String(), "Unknown setting “colour”.")
}

func TestConfigSetUnknownKeyIsReportedBeforeTheConfigIsRead(t *testing.T) {
	h := newHarness(t)
	// A broken file must not hide the real mistake in how the command was
	// typed: the key is judged first.
	h.writeConfig("language: fr\n")

	require.Equal(t, 2, h.run("config", "set", "colour", "red"))
	require.Contains(t, h.stderr.String(), "Unknown setting “colour”.")
}

func TestConfigSetFolderWithoutTeamFails(t *testing.T) {
	h := newHarness(t)

	code := h.run("config", "set", "folder", "newsletter")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t,
		"kurze-url: "+h.configPath+": “folder” belongs to one team. Add --team SLUG.\n",
		h.stderr.String())
	require.NoFileExists(t, h.configPath)
}

func TestConfigSetNeverFallsBackToTheDefaultTeam(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("team: sv-gruenwald\n")

	code := h.run("config", "set", "folder", "newsletter")

	// Writing is explicit: the default team is a convenience for reading,
	// and a setting that lands in the wrong section is easy to miss.
	require.Equal(t, 1, code)
	require.Contains(t, h.stderr.String(), "belongs to one team")
	require.Equal(t, "team: sv-gruenwald\n", h.config())
}

func TestConfigSetGlobalKeyForATeamFails(t *testing.T) {
	h := newHarness(t)

	code := h.run("config", "set", "language", "de", "--team", "sv-gruenwald")

	require.Equal(t, 1, code)
	require.Contains(t, h.stderr.String(), "“language” applies to every team")
	require.NoFileExists(t, h.configPath)
}

func TestConfigSetBadSizeNamesTheFile(t *testing.T) {
	h := newHarness(t)

	code := h.run("config", "set", "qr.size", "10")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t,
		"kurze-url: "+h.configPath+": “qr.size” must be a whole number from 64 to 2048, not “10”.\n",
		h.stderr.String())
	require.NoFileExists(t, h.configPath, "a refused value writes nothing")
}

func TestConfigSetBadTeamSlugIsRefused(t *testing.T) {
	h := newHarness(t)

	require.Equal(t, 1, h.run("config", "set", "qr.format", "png", "--team", "Not A Slug"))

	require.Contains(t, h.stderr.String(), "is not a valid team slug")
	require.NoFileExists(t, h.configPath)
}

func TestConfigSetInGerman(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("language: de\n")

	require.Equal(t, 1, h.run("config", "set", "qr.size", "10"))
	require.Equal(t,
		"kurze-url: "+h.configPath+": „qr.size“ muss eine ganze Zahl von 64 bis 2048 sein, nicht „10“.\n",
		h.stderr.String())

	h.resetOutput()
	require.Equal(t, 0, h.run("config", "set", "qr.size", "512"))
	require.Equal(t, "Gespeichert in "+h.configPath+"\n", h.stderr.String())
}

func TestConfigSetKeepsWhatIsAlreadyInTheFile(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("# my settings\nlanguage: en\n")

	require.Equal(t, 0, h.run("config", "set", "team", "sv-gruenwald"), h.stderr.String())

	require.Equal(t, "# my settings\nlanguage: en\nteam: sv-gruenwald\n", h.config())
}

func TestConfigSetOverwritesAValue(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("qr:\n  format: svg\n")

	require.Equal(t, 0, h.run("config", "set", "qr.format", "png"), h.stderr.String())

	require.Equal(t, "qr:\n  format: png\n", h.config())
}

func TestConfigSetRefusesABrokenConfigFileAndLeavesItAlone(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("qr:\n  size: 5000\n")

	code := h.run("config", "set", "qr.format", "png")

	require.Equal(t, 1, code)
	require.Equal(t,
		"kurze-url: "+h.configPath+":2: “qr.size” must be a whole number from 64 to 2048, not “5000”.\n",
		h.stderr.String())
	require.Equal(t, "qr:\n  size: 5000\n", h.config())
}

func TestConfigUnsetRemovesTheKey(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("team: sv-gruenwald\nqr:\n  format: png\n  size: 1024\n")

	code := h.run("config", "unset", "qr.size")

	require.Equal(t, 0, code, h.stderr.String())
	require.Empty(t, h.stdout.String())
	require.Equal(t, "Saved to "+h.configPath+"\n", h.stderr.String())
	require.Equal(t, "team: sv-gruenwald\nqr:\n  format: png\n", h.config())

	h.resetOutput()
	require.Equal(t, 1, h.run("config", "get", "qr.size"))
}

func TestConfigUnsetRemovesTheSectionsItEmptied(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    qr:\n      fg: '1a3a6e'\n")

	require.Equal(t, 0, h.run("config", "unset", "qr.fg", "--team", "sv-gruenwald"), h.stderr.String())

	require.Equal(t, "team: sv-gruenwald\n", h.config())
}

func TestConfigUnsetLeavesTheOtherScopeAlone(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("qr:\n  format: png\nteams:\n  sv-gruenwald:\n    qr:\n      format: svg\n")

	require.Equal(t, 0, h.run("config", "unset", "qr.format", "--team", "sv-gruenwald"), h.stderr.String())

	require.Equal(t, "qr:\n  format: png\n", h.config())
}

func TestConfigUnsetOfAnAbsentKeyWritesNothing(t *testing.T) {
	h := newHarness(t)

	code := h.run("config", "unset", "qr.fg")

	// Run twice, it must not fail the second time, and it must not create an
	// empty file in a place where the person has none.
	require.Equal(t, 0, code, h.stderr.String())
	require.Empty(t, h.stdout.String())
	require.Equal(t, "“qr.fg” is not set.\n", h.stderr.String())
	require.NoFileExists(t, h.configPath)
}

func TestConfigUnsetOfAnAbsentKeyLeavesTheFileUntouched(t *testing.T) {
	h := newHarness(t)
	const original = "# keep me\n\nteam:    sv-gruenwald\n"
	h.writeConfig(original)
	before, err := os.Stat(h.configPath)
	require.NoError(t, err)

	require.Equal(t, 0, h.run("config", "unset", "qr.fg"), h.stderr.String())

	// Not even re-encoded: the odd spacing would be normalised by a save.
	require.Equal(t, original, h.config())
	after, err := os.Stat(h.configPath)
	require.NoError(t, err)
	require.Equal(t, before.ModTime(), after.ModTime())
}

func TestConfigUnsetFolderWithoutTeamFails(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("team: sv-gruenwald\n")

	code := h.run("config", "unset", "folder")

	// The scope is judged before "was there anything": a wrong scope is a
	// mistake to report, not a quiet no-op.
	require.Equal(t, 1, code)
	require.Contains(t, h.stderr.String(), "“folder” belongs to one team. Add --team SLUG.")
}

func TestConfigGroupAndArgumentsAreUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"bare group":        {"config"},
		"unknown subcmd":    {"config", "nope"},
		"get two keys":      {"config", "get", "qr.fg", "qr.bg"},
		"set without value": {"config", "set", "qr.fg"},
		"set extra":         {"config", "set", "qr.fg", "ffffff", "000000"},
		"unset without key": {"config", "unset"},
		"unset extra":       {"config", "unset", "qr.fg", "qr.bg"},
		"unknown flag":      {"config", "get", "--colour"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			require.Equal(t, 2, h.run(args...), h.stderr.String())
			require.Empty(t, h.stdout.String())
			require.NoFileExists(t, h.configPath)
		})
	}
}

func TestBuildingTheConfigGroupReadsNoConfigFile(t *testing.T) {
	h := newHarness(t)
	// The config is broken, and `version` and `completion` must still work,
	// because that is when a person reaches for them. The group's commands
	// build their App in RunE and never while the tree is assembled.
	h.writeConfig("language: fr\n")

	require.Equal(t, 0, h.run("version"), h.stderr.String())
	require.Equal(t, 0, h.run("completion", "zsh"), h.stderr.String())
}
