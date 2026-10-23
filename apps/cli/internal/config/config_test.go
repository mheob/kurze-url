package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/config"
)

// writeConfig puts content into a config.yaml inside a fresh temporary
// directory and returns its path. No test goes near the real config
// directory: every path in this file comes from t.TempDir.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// loadConfig loads content as a config file and fails the test if it is
// refused.
func loadConfig(t *testing.T, content string) *config.File {
	t.Helper()
	f, err := config.Load(writeConfig(t, content))
	require.NoError(t, err)
	return f
}

// asValidationError unwraps err into the typed error the CLI layer
// translates, failing the test when it is anything else.
func asValidationError(t *testing.T, err error) *config.ValidationError {
	t.Helper()
	var verr *config.ValidationError
	require.ErrorAs(t, err, &verr)
	return verr
}

// saveAndRead saves f and returns what ended up on disk.
func saveAndRead(t *testing.T, f *config.File, path string) string {
	t.Helper()
	require.NoError(t, f.Save())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kurze-url", "config.yaml")

	f, err := config.Load(path)
	require.NoError(t, err)

	for _, k := range config.Keys() {
		_, ok := f.Get(k, "")
		require.False(t, ok, "global %s", k)
		_, ok = f.Get(k, "sv-gruenwald")
		require.False(t, ok, "team %s", k)
	}
	require.Empty(t, f.TeamSections())
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "Load must not create the file")
}

func TestLoadEmptyAndCommentOnlyFilesAreValid(t *testing.T) {
	for name, content := range map[string]string{
		"empty":        "",
		"blank":        "\n\n",
		"comment only": "# nothing here yet\n",
		"bare marker":  "---\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := loadConfig(t, content)
			require.Empty(t, f.TeamSections())
			_, ok := f.Get(config.Language, "")
			require.False(t, ok)
		})
	}
}

func TestResolvePrefersTeamOverGlobal(t *testing.T) {
	f := loadConfig(t, `language: de
qr:
  format: png
  size: 1024
teams:
  sv-gruenwald:
    folder: newsletter
    qr:
      format: svg
`)

	value, source, ok := f.Resolve(config.QRFormat, "sv-gruenwald")
	require.True(t, ok)
	require.Equal(t, "svg", value)
	require.Equal(t, config.SourceTeam, source)

	value, source, ok = f.Resolve(config.QRFormat, "tsv-musterstadt")
	require.True(t, ok)
	require.Equal(t, "png", value)
	require.Equal(t, config.SourceGlobal, source)

	value, source, ok = f.Resolve(config.QRFormat, "")
	require.True(t, ok)
	require.Equal(t, "png", value)
	require.Equal(t, config.SourceGlobal, source)

	t.Run("a team section without the key falls back to global", func(t *testing.T) {
		value, source, ok := f.Resolve(config.QRSize, "sv-gruenwald")
		require.True(t, ok)
		require.Equal(t, "1024", value)
		require.Equal(t, config.SourceGlobal, source)
	})

	t.Run("a global-only key ignores the team", func(t *testing.T) {
		value, source, ok := f.Resolve(config.Language, "sv-gruenwald")
		require.True(t, ok)
		require.Equal(t, "de", value)
		require.Equal(t, config.SourceGlobal, source)
	})

	t.Run("a per-team key has no global fallback", func(t *testing.T) {
		value, source, ok := f.Resolve(config.Folder, "sv-gruenwald")
		require.True(t, ok)
		require.Equal(t, "newsletter", value)
		require.Equal(t, config.SourceTeam, source)

		_, _, ok = f.Resolve(config.Folder, "tsv-musterstadt")
		require.False(t, ok)
		_, _, ok = f.Resolve(config.Folder, "")
		require.False(t, ok)
	})

	t.Run("nothing set is not ok", func(t *testing.T) {
		_, _, ok := f.Resolve(config.QRFG, "sv-gruenwald")
		require.False(t, ok)
	})
}

func TestGetReadsOnlyThatScope(t *testing.T) {
	f := loadConfig(t, `team: sv-gruenwald
qr:
  fg: '000000'
teams:
  sv-gruenwald:
    domain: links.sv-gruenwald.de
`)

	value, ok := f.Get(config.Team, "")
	require.True(t, ok)
	require.Equal(t, "sv-gruenwald", value)

	value, ok = f.Get(config.QRFG, "")
	require.True(t, ok)
	require.Equal(t, "000000", value)

	_, ok = f.Get(config.QRFG, "sv-gruenwald")
	require.False(t, ok, "the team section has no fg of its own")

	value, ok = f.Get(config.Domain, "sv-gruenwald")
	require.True(t, ok)
	require.Equal(t, "links.sv-gruenwald.de", value)

	_, ok = f.Get(config.Domain, "")
	require.False(t, ok, "domain has no global scope")
	_, ok = f.Get(config.Team, "sv-gruenwald")
	require.False(t, ok, "team has no per-team scope")
}

func TestLoadAcceptsColourAndSizeWhateverTheirTag(t *testing.T) {
	// YAML reads 000000 as an integer, 'FFFFFF' as an upper-case string and
	// '1024' as a string: the rules judge the text, not the tag.
	f := loadConfig(t, `qr:
  fg: 000000
  bg: 'FFFFFF'
  size: '1024'
`)

	for key, want := range map[config.Key]string{
		config.QRFG:   "000000",
		config.QRBG:   "FFFFFF",
		config.QRSize: "1024",
	} {
		got, ok := f.Get(key, "")
		require.True(t, ok, key)
		require.Equal(t, want, got, key)
	}
}

func TestLoadAcceptsTheEdgesOfEveryRange(t *testing.T) {
	f := loadConfig(t, `team: abc
qr:
  size: 64
teams:
  `+strings.Repeat("a", 40)+`:
    domain: a-1.b
    qr:
      size: 2048
      format: png
      fg: aBcDeF
`)

	require.Equal(t, []string{strings.Repeat("a", 40)}, f.TeamSections())
	value, ok := f.Get(config.QRSize, "")
	require.True(t, ok)
	require.Equal(t, "64", value)
	value, ok = f.Get(config.QRSize, strings.Repeat("a", 40))
	require.True(t, ok)
	require.Equal(t, "2048", value)
}

func TestLoadTreatsAQuotedNullAsText(t *testing.T) {
	// A bare null is "no value"; a person who really wants the slug null, or
	// a folder called ~, quotes it.
	f := loadConfig(t, "team: 'null'\nteams:\n  sv-gruenwald:\n    folder: '~'\n")

	value, ok := f.Get(config.Team, "")
	require.True(t, ok)
	require.Equal(t, "null", value)
	value, ok = f.Get(config.Folder, "sv-gruenwald")
	require.True(t, ok)
	require.Equal(t, "~", value)
}

func TestLoadRejects(t *testing.T) {
	longSlug := strings.Repeat("a", 41)

	tests := []struct {
		name    string
		content string
		code    config.Code
		line    int
		key     string
	}{
		{"unknown top-level key", "language: en\nbogus: 1\n", config.CodeUnknownKey, 2, "bogus"},
		{"per-team key at the top level", "folder: newsletter\n", config.CodeUnknownKey, 1, "folder"},
		{"unknown qr key", "qr:\n  colour: ff0000\n", config.CodeUnknownKey, 2, "qr.colour"},
		{"unknown team key", "teams:\n  sv-gruenwald:\n    bogus: x\n", config.CodeUnknownKey, 3, "teams.sv-gruenwald.bogus"},
		{"global-only key in a team", "teams:\n  sv-gruenwald:\n    language: de\n", config.CodeUnknownKey, 3, "teams.sv-gruenwald.language"},
		{"merge key", "<<: {}\n", config.CodeUnknownKey, 1, "<<"},

		{"duplicate key", "language: en\nlanguage: de\n", config.CodeDuplicateKey, 2, "language"},
		{"duplicate team section", "teams:\n  sv-gruenwald: {}\n  sv-gruenwald: {}\n", config.CodeDuplicateKey, 3, "teams.sv-gruenwald"},

		{"team section that is null", "teams: {sv-gruenwald: ~}\n", config.CodeNotAMapping, 1, "teams.sv-gruenwald"},
		{"team section left empty", "teams:\n  sv-gruenwald:\n", config.CodeNotAMapping, 2, "teams.sv-gruenwald"},
		{"qr that is a scalar", "qr: 5\n", config.CodeNotAMapping, 1, "qr"},
		{"teams that is a list", "teams:\n  - sv-gruenwald\n", config.CodeNotAMapping, 2, "teams"},
		{"document that is a list", "- a\n- b\n", config.CodeNotAMapping, 1, ""},
		{"document that is a scalar", "foo\n", config.CodeNotAMapping, 1, ""},
		{"alias where a mapping belongs", "qr: &q\n  format: png\nteams:\n  sv-gruenwald:\n    qr: *q\n", config.CodeNotAMapping, 5, "teams.sv-gruenwald.qr"},

		{"language fr", "language: fr\n", config.CodeInvalidLanguage, 1, "language"},
		{"language in capitals", "language: DE\n", config.CodeInvalidLanguage, 1, "language"},
		{"language that is a list", "language: [en]\n", config.CodeInvalidLanguage, 1, "language"},

		{"team slug in capitals", "teams:\n  SV:\n    folder: x\n", config.CodeInvalidTeamSlug, 2, "teams.SV"},
		{"default team too short", "team: ab\n", config.CodeInvalidTeamSlug, 1, "team"},
		{"default team too long", "team: " + longSlug + "\n", config.CodeInvalidTeamSlug, 1, "team"},
		{"default team with a leading hyphen", "team: -abc\n", config.CodeInvalidTeamSlug, 1, "team"},
		{"default team that is null", "team: null\n", config.CodeInvalidTeamSlug, 1, "team"},

		{"format jpg", "qr:\n  format: jpg\n", config.CodeInvalidFormat, 2, "qr.format"},
		{"format in capitals", "qr:\n  format: PNG\n", config.CodeInvalidFormat, 2, "qr.format"},

		{"size 63", "qr:\n  size: 63\n", config.CodeInvalidSize, 2, "qr.size"},
		{"size 2049", "qr:\n  size: 2049\n", config.CodeInvalidSize, 2, "qr.size"},
		{"size that is not a number", "qr:\n  size: big\n", config.CodeInvalidSize, 2, "qr.size"},
		{"size that is a fraction", "qr:\n  size: 128.5\n", config.CodeInvalidSize, 2, "qr.size"},
		{"size with a sign", "qr:\n  size: +128\n", config.CodeInvalidSize, 2, "qr.size"},

		{"colour of five digits", "qr:\n  fg: 12345\n", config.CodeInvalidColour, 2, "qr.fg"},
		{"colour with a hash", "qr:\n  bg: '#ffffff'\n", config.CodeInvalidColour, 2, "qr.bg"},
		{"colour that is not hex", "teams:\n  sv-gruenwald:\n    qr:\n      fg: gggggg\n", config.CodeInvalidColour, 4, "teams.sv-gruenwald.qr.fg"},

		{"domain in capitals", "teams:\n  sv-gruenwald:\n    domain: Links.Example.de\n", config.CodeInvalidHostname, 3, "teams.sv-gruenwald.domain"},
		{"domain with a path", "teams:\n  sv-gruenwald:\n    domain: links.example.de/x\n", config.CodeInvalidHostname, 3, "teams.sv-gruenwald.domain"},

		{"empty folder", "teams:\n  sv-gruenwald:\n    folder: ''\n", config.CodeEmptyValue, 3, "teams.sv-gruenwald.folder"},
		{"folder that is null", "teams:\n  sv-gruenwald:\n    folder: ~\n", config.CodeEmptyValue, 3, "teams.sv-gruenwald.folder"},
		{"folder of spaces", "teams:\n  sv-gruenwald:\n    folder: '   '\n", config.CodeEmptyValue, 3, "teams.sv-gruenwald.folder"},
		{"folder given no value", "teams:\n  sv-gruenwald:\n    folder:\n", config.CodeEmptyValue, 3, "teams.sv-gruenwald.folder"},
		{"empty domain", "teams:\n  sv-gruenwald:\n    domain: ''\n", config.CodeEmptyValue, 3, "teams.sv-gruenwald.domain"},

		{"broken yaml on line 3", "language: de\nteam: sv-gruenwald\n  bad: : value\n", config.CodeSyntax, 3, ""},
		// The line is the second document's own `---`, where the unread part begins.
		{"second document", "language: de\n---\nteam: sv-gruenwald\n", config.CodeSyntax, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.content)

			f, err := config.Load(path)

			require.Nil(t, f)
			verr := asValidationError(t, err)
			require.Equal(t, tc.code, verr.Code)
			require.Equal(t, tc.line, verr.Line)
			require.Equal(t, path, verr.Path)
			require.Equal(t, tc.key, verr.Key)
		})
	}
}

func TestLoadReportsTheOffendingValue(t *testing.T) {
	_, err := config.Load(writeConfig(t, "qr:\n  size: 63\n"))
	require.Equal(t, "63", asValidationError(t, err).Value)

	_, err = config.Load(writeConfig(t, "teams:\n  SV: {}\n"))
	require.Equal(t, "SV", asValidationError(t, err).Value)
}

func TestLoadReturnsPlainErrorsForUnreadableFiles(t *testing.T) {
	_, err := config.Load(t.TempDir()) // a directory, not a file

	require.Error(t, err)
	var verr *config.ValidationError
	require.False(t, errors.As(err, &verr), "an I/O failure is not a validation failure")
}

func TestValidationErrorMessage(t *testing.T) {
	require.Equal(t, "/x/config.yaml:3: invalid_size",
		(&config.ValidationError{Path: "/x/config.yaml", Line: 3, Code: config.CodeInvalidSize}).Error())
	require.Equal(t, "/x/config.yaml: key_needs_team",
		(&config.ValidationError{Path: "/x/config.yaml", Code: config.CodeKeyNeedsTeam}).Error())
	require.Equal(t, "unknown_key",
		(&config.ValidationError{Code: config.CodeUnknownKey}).Error())
}

func TestParseKey(t *testing.T) {
	tests := []struct {
		key     config.Key
		global  bool
		perTeam bool
	}{
		{config.Language, true, false},
		{config.Team, true, false},
		{config.Folder, false, true},
		{config.Domain, false, true},
		{config.QRFormat, true, true},
		{config.QRSize, true, true},
		{config.QRFG, true, true},
		{config.QRBG, true, true},
	}
	for _, tc := range tests {
		t.Run(string(tc.key), func(t *testing.T) {
			parsed, err := config.ParseKey(string(tc.key))
			require.NoError(t, err)
			require.Equal(t, tc.key, parsed)
			require.Equal(t, tc.global, parsed.Global())
			require.Equal(t, tc.perTeam, parsed.PerTeam())
		})
	}

	require.Equal(t, "qr.format", string(config.QRFormat))
	require.Len(t, config.Keys(), len(tests))

	for _, s := range []string{"", "bogus", "QR.FORMAT", "qr", "qr.", "teams", " language"} {
		_, err := config.ParseKey(s)
		verr := asValidationError(t, err)
		require.Equal(t, config.CodeUnknownKey, verr.Code, "%q", s)
		require.Equal(t, s, verr.Key)
	}
}

func TestTeamSectionsKeepFileOrder(t *testing.T) {
	f := loadConfig(t, `teams:
  zzz-team: {}
  aaa-team:
    folder: x
  mmm-team: {}
`)

	require.Equal(t, []string{"zzz-team", "aaa-team", "mmm-team"}, f.TeamSections())
}

func TestSetKeepsCommentsAndOrder(t *testing.T) {
	path := writeConfig(t, `# my defaults
language: de
team: sv-gruenwald # my team
`)
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Set(config.QRFG, "sv-gruenwald", "#1A3A6E"))
	text := saveAndRead(t, f, path)

	require.Contains(t, text, "# my defaults")
	require.Contains(t, text, "# my team")
	require.Less(t, strings.Index(text, "language:"), strings.Index(text, "team:"))
	require.Contains(t, text, "fg: '1a3a6e'")
	require.Equal(t, `# my defaults
language: de
team: sv-gruenwald # my team
teams:
  sv-gruenwald:
    qr:
      fg: '1a3a6e'
`, text)

	reloaded, err := config.Load(path)
	require.NoError(t, err)
	value, source, ok := reloaded.Resolve(config.QRFG, "sv-gruenwald")
	require.True(t, ok)
	require.Equal(t, "1a3a6e", value)
	require.Equal(t, config.SourceTeam, source)
}

func TestSetKeepsAHeadCommentSeparatedByABlankLine(t *testing.T) {
	path := writeConfig(t, "# my defaults\n\nlanguage: de\n")
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Set(config.Team, "", "sv-gruenwald"))

	require.Equal(t, "# my defaults\n\nlanguage: de\nteam: sv-gruenwald\n", saveAndRead(t, f, path))
}

func TestSetOverwritesInPlaceAndKeepsTheLineComment(t *testing.T) {
	path := writeConfig(t, "team: sv-gruenwald # my team\nqr:\n  fg: \"000000\" # black\n")
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Set(config.Team, "", "tsv-musterstadt"))
	require.NoError(t, f.Set(config.QRFG, "", "ffffff"))

	require.Equal(t, "team: tsv-musterstadt # my team\nqr:\n  fg: 'ffffff' # black\n", saveAndRead(t, f, path))
}

func TestSetWritesEachValueInItsOwnShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Set(config.QRSize, "", "01024"))
	require.NoError(t, f.Set(config.QRBG, "", "#FFFFFF"))
	require.NoError(t, f.Set(config.QRFormat, "", "png"))
	require.NoError(t, f.Set(config.Language, "", "de"))
	require.NoError(t, f.Set(config.Team, "", "123"))
	require.NoError(t, f.Set(config.Folder, "sv-gruenwald", "Newsletter 2026"))
	require.NoError(t, f.Set(config.Domain, "sv-gruenwald", "links.sv-gruenwald.de"))

	text := saveAndRead(t, f, path)
	require.Contains(t, text, "\n  size: 1024\n", "size is a plain integer")
	require.Contains(t, text, "\n  bg: 'ffffff'\n", "a colour is single-quoted and lower-cased")
	require.Contains(t, text, "\n  format: png\n")
	require.Contains(t, text, "language: de\n")
	require.Contains(t, text, "\nteam: \"123\"\n", "a slug that looks like a number stays a string")
	require.Contains(t, text, "\n    folder: Newsletter 2026\n")

	reloaded, err := config.Load(path)
	require.NoError(t, err, "what Set writes must load again")
	value, ok := reloaded.Get(config.Team, "")
	require.True(t, ok)
	require.Equal(t, "123", value)
	value, ok = reloaded.Get(config.QRSize, "")
	require.True(t, ok)
	require.Equal(t, "1024", value)
}

func TestSetValidatesExactlyAsLoadDoes(t *testing.T) {
	tests := []struct {
		name  string
		key   config.Key
		team  string
		value string
		code  config.Code
		field string
		bad   string // the Value the error is expected to carry
	}{
		{"language in capitals", config.Language, "", "DE", config.CodeInvalidLanguage, "language", "DE"},
		{"language fr", config.Language, "", "fr", config.CodeInvalidLanguage, "language", "fr"},
		{"empty language", config.Language, "", "", config.CodeInvalidLanguage, "language", ""},
		{"short team", config.Team, "", "ab", config.CodeInvalidTeamSlug, "team", "ab"},
		{"team in capitals", config.Team, "", "SV", config.CodeInvalidTeamSlug, "team", "SV"},
		{"format jpg", config.QRFormat, "", "jpg", config.CodeInvalidFormat, "qr.format", "jpg"},
		{"size 63", config.QRSize, "", "63", config.CodeInvalidSize, "qr.size", "63"},
		{"size 2049", config.QRSize, "", "2049", config.CodeInvalidSize, "qr.size", "2049"},
		{"size in words", config.QRSize, "", "big", config.CodeInvalidSize, "qr.size", "big"},
		{"colour of five digits", config.QRFG, "", "12345", config.CodeInvalidColour, "qr.fg", "12345"},
		{"colour with two hashes", config.QRFG, "", "##123456", config.CodeInvalidColour, "qr.fg", "##123456"},
		{"colour that is not hex", config.QRBG, "sv-gruenwald", "ggg", config.CodeInvalidColour, "teams.sv-gruenwald.qr.bg", "ggg"},
		{"empty folder", config.Folder, "sv-gruenwald", "", config.CodeEmptyValue, "teams.sv-gruenwald.folder", ""},
		{"folder of spaces", config.Folder, "sv-gruenwald", "  ", config.CodeEmptyValue, "teams.sv-gruenwald.folder", "  "},
		{"empty domain", config.Domain, "sv-gruenwald", "", config.CodeEmptyValue, "teams.sv-gruenwald.domain", ""},
		{"domain in capitals", config.Domain, "sv-gruenwald", "Links.de", config.CodeInvalidHostname, "teams.sv-gruenwald.domain", "Links.de"},
		{"team section named in capitals", config.QRFG, "SV", "ffffff", config.CodeInvalidTeamSlug, "teams.SV", "SV"},
		{"key the file does not know", config.Key("bogus"), "", "x", config.CodeUnknownKey, "bogus", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			f, err := config.Load(path)
			require.NoError(t, err)

			err = f.Set(tc.key, tc.team, tc.value)

			verr := asValidationError(t, err)
			require.Equal(t, tc.code, verr.Code)
			require.Equal(t, path, verr.Path)
			require.Equal(t, tc.field, verr.Key)
			require.Equal(t, tc.bad, verr.Value, "the value as the caller passed it")
			require.Zero(t, verr.Line, "a value that never reached the file has no line")

			require.NoError(t, f.Save())
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Empty(t, string(data), "a refused Set must not leave a half-built section behind")
		})
	}
}

func TestSetRoundTripsAwkwardFolderNames(t *testing.T) {
	// Folder names are free text the encoder has to quote correctly: a name
	// YAML would read as another type or as syntax must come back as itself.
	for _, name := range []string{
		"Newsletter: Herbst 2026", "# Wichtig", "true", "123", "null", "~",
		`a 'b' "c"`, "  padded  ", "multi\nline", "Größe ä ö ü", "- dash", "{braces}",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			f, err := config.Load(path)
			require.NoError(t, err)
			require.NoError(t, f.Set(config.Folder, "sv-gruenwald", name))
			require.NoError(t, f.Save())

			reloaded, err := config.Load(path)
			require.NoError(t, err, "what Set writes must load again")
			got, ok := reloaded.Get(config.Folder, "sv-gruenwald")
			require.True(t, ok)
			require.Equal(t, name, got)
		})
	}
}

func TestSetRefusesKeyInWrongScope(t *testing.T) {
	f := loadConfig(t, "")

	err := f.Set(config.Folder, "", "x")
	require.Equal(t, config.CodeKeyNeedsTeam, asValidationError(t, err).Code)
	err = f.Set(config.Domain, "", "links.example.de")
	require.Equal(t, config.CodeKeyNeedsTeam, asValidationError(t, err).Code)

	err = f.Set(config.Language, "sv-gruenwald", "de")
	require.Equal(t, config.CodeKeyIsGlobal, asValidationError(t, err).Code)
	err = f.Set(config.Team, "sv-gruenwald", "sv-gruenwald")
	require.Equal(t, config.CodeKeyIsGlobal, asValidationError(t, err).Code)

	require.Equal(t, "folder", asValidationError(t, f.Set(config.Folder, "", "x")).Key)

	require.NoError(t, f.Set(config.QRFormat, "", "svg"), "qr.* lives in both scopes")
	require.NoError(t, f.Set(config.QRFormat, "sv-gruenwald", "png"))
}

func TestUnsetRefusesKeyInWrongScope(t *testing.T) {
	f := loadConfig(t, "language: de\n")

	require.Equal(t, config.CodeKeyNeedsTeam, asValidationError(t, f.Unset(config.Folder, "")).Code)
	require.Equal(t, config.CodeKeyIsGlobal, asValidationError(t, f.Unset(config.Language, "sv-gruenwald")).Code)
	require.Equal(t, config.CodeInvalidTeamSlug, asValidationError(t, f.Unset(config.QRFG, "SV")).Code)
}

func TestUnsetRemovesEmptyParents(t *testing.T) {
	path := writeConfig(t, `teams:
  sv-gruenwald:
    qr:
      fg: '1a3a6e'
`)
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Unset(config.QRFG, "sv-gruenwald"))

	require.Empty(t, f.TeamSections())
	require.Equal(t, "", saveAndRead(t, f, path), "an emptied config is an empty document, not {}")
	reloaded, err := config.Load(path)
	require.NoError(t, err)
	require.Empty(t, reloaded.TeamSections())
}

func TestUnsetStopsAtTheFirstParentThatStillHasContent(t *testing.T) {
	path := writeConfig(t, `qr:
  format: png
  size: 512
teams:
  sv-gruenwald:
    folder: newsletter
    qr:
      fg: '1a3a6e'
  tsv-musterstadt:
    qr:
      bg: ffffff
`)
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Unset(config.QRSize, ""))
	require.NoError(t, f.Unset(config.QRFG, "sv-gruenwald"))
	require.NoError(t, f.Unset(config.QRBG, "tsv-musterstadt"))

	require.Equal(t, `qr:
  format: png
teams:
  sv-gruenwald:
    folder: newsletter
`, saveAndRead(t, f, path))
}

func TestUnsetOfAnAbsentKeyChangesNothing(t *testing.T) {
	path := writeConfig(t, "# keep\nlanguage: de\n")
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Unset(config.QRFG, ""))
	require.NoError(t, f.Unset(config.QRFG, "sv-gruenwald"))
	require.NoError(t, f.Unset(config.Folder, "sv-gruenwald"))

	require.Equal(t, "# keep\nlanguage: de\n", saveAndRead(t, f, path))
}

func TestUnsetKeepsTheDocumentsOwnComments(t *testing.T) {
	path := writeConfig(t, "# my defaults\n\nlanguage: de\n")
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Unset(config.Language, ""))

	require.Equal(t, "# my defaults\n", saveAndRead(t, f, path))
}

func TestACommentOnlyFileSurvivesAnEdit(t *testing.T) {
	path := writeConfig(t, "# nothing here yet\n")
	f, err := config.Load(path)
	require.NoError(t, err)

	require.NoError(t, f.Set(config.Language, "", "de"))

	require.Equal(t, "# nothing here yet\n\nlanguage: de\n", saveAndRead(t, f, path))
}

func TestPathHonoursOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "elsewhere.yaml")

	got, err := config.Path(func(name string) string {
		if name == "KURZE_URL_CONFIG" {
			return override
		}
		return ""
	})

	require.NoError(t, err)
	require.Equal(t, override, got)
}

func TestPathFallsBackToTheUserConfigDirectory(t *testing.T) {
	// os.UserConfigDir reads these per platform; pointing them all into a
	// temporary directory keeps the answer off the real one.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)

	got, err := config.Path(func(string) string { return "" })

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, home), "%s is not under %s", got, home)
	require.Equal(t, "config.yaml", filepath.Base(got))
	require.Equal(t, "kurze-url", filepath.Base(filepath.Dir(got)))
}

func TestSaveCreatesDirectoryWith0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits")
	}
	dir := filepath.Join(t.TempDir(), "nested", "kurze-url")
	path := filepath.Join(dir, "config.yaml")
	f, err := config.Load(path)
	require.NoError(t, err)
	require.NoError(t, f.Set(config.Language, "", "en"))

	require.NoError(t, f.Save())

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	parentInfo, err := os.Stat(filepath.Dir(dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), parentInfo.Mode().Perm())
	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fileInfo.Mode().Perm())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the temporary file must not outlive the rename")
}

func TestSaveReplacesAnExistingFile(t *testing.T) {
	path := writeConfig(t, "language: de\n")
	f, err := config.Load(path)
	require.NoError(t, err)
	require.NoError(t, f.Set(config.Language, "", "en"))

	require.Equal(t, "language: en\n", saveAndRead(t, f, path))
}

func TestSaveWritesThroughASymlink(t *testing.T) {
	// Dotfile managers keep the real file elsewhere and link it into place;
	// a rename over the link would silently replace the link with a copy.
	linked := writeConfig(t, "language: de\n")
	link := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Symlink(linked, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	f, err := config.Load(link)
	require.NoError(t, err)
	require.NoError(t, f.Set(config.Team, "", "sv-gruenwald"))

	require.NoError(t, f.Save())

	info, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink, "the link must still be a link")
	data, err := os.ReadFile(linked)
	require.NoError(t, err)
	require.Equal(t, "language: de\nteam: sv-gruenwald\n", string(data))
}
