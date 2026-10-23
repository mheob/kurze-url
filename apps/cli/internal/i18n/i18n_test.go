package i18n

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// mustMessages reads one shipped catalog through the same path Load uses, so
// the tests below judge exactly the bytes that get embedded.
func mustMessages(t *testing.T, l Lang) map[string]string {
	t.Helper()
	messages, err := loadMessages(l)
	require.NoError(t, err)
	return messages
}

// keysMissingFrom returns the keys of a that b does not have, sorted.
func keysMissingFrom(a, b map[string]string) []string {
	var missing []string
	for key := range a {
		if _, ok := b[key]; !ok {
			missing = append(missing, key)
		}
	}
	slices.Sort(missing)
	return missing
}

// placeholdersOf returns the distinct {name} tokens of s, sorted.
func placeholdersOf(s string) []string {
	var names []string
	for _, m := range placeholderPattern.FindAllStringSubmatch(s, -1) {
		names = append(names, m[1])
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// placeholderMismatches lists, sorted, every key both catalogs have whose
// text carries a different set of {name} tokens in the two languages. A key
// only one catalog has is the key-set test's finding, not this one's.
func placeholderMismatches(en, de map[string]string) []string {
	var mismatches []string
	for key, enText := range en {
		deText, ok := de[key]
		if !ok {
			continue
		}
		if !slices.Equal(placeholdersOf(enText), placeholdersOf(deText)) {
			mismatches = append(mismatches, fmt.Sprintf("%s: en has %v, de has %v",
				key, placeholdersOf(enText), placeholdersOf(deText)))
		}
	}
	slices.Sort(mismatches)
	return mismatches
}

func TestCatalogsHaveTheSameKeys(t *testing.T) {
	en, de := mustMessages(t, EN), mustMessages(t, DE)

	require.Empty(t, keysMissingFrom(en, de), "keys in en.json that de.json lacks")
	require.Empty(t, keysMissingFrom(de, en), "keys in de.json that en.json lacks")
}

func TestCatalogsHaveTheSamePlaceholders(t *testing.T) {
	en, de := mustMessages(t, EN), mustMessages(t, DE)

	require.Empty(t, placeholderMismatches(en, de))
}

// The two parity checks above run over catalogs that start empty, so on their
// own they would pass whatever the helpers did. This pins that the helpers
// really notice a difference.
func TestParityChecksNoticeADifference(t *testing.T) {
	en := map[string]string{
		"only.en":  "English only",
		"greeting": "Hello {name}",
		"count":    "{n} of {total}",
		"same":     "Nothing to fill in",
	}
	de := map[string]string{
		"only.de":  "Nur Deutsch",
		"greeting": "Hallo {nombre}",
		"count":    "{n} von {n} und {total}",
		"same":     "Nichts einzusetzen",
	}

	require.Equal(t, []string{"only.en"}, keysMissingFrom(en, de))
	require.Equal(t, []string{"only.de"}, keysMissingFrom(de, en))
	require.Equal(t, []string{
		"greeting: en has [name], de has [nombre]",
	}, placeholderMismatches(en, de), "a repeated token is one placeholder, not two")
}

func TestTReplacesPlaceholders(t *testing.T) {
	c := newCatalog(EN, map[string]string{"greeting": "Hello {name}"})

	require.Equal(t, "Hello Ada", c.T("greeting", Args{"name": "Ada"}))
}

func TestT(t *testing.T) {
	c := newCatalog(DE, map[string]string{
		"plain":    "Fertig",
		"greeting": "Hallo {name}",
		"twice":    "{a} und {a}",
		"pair":     "{a} {b}",
	})

	tests := []struct {
		name string
		key  string
		args []Args
		want string
	}{
		{name: "no placeholders", key: "plain", want: "Fertig"},
		{name: "unknown key is returned as it is", key: "no.such.key", want: "no.such.key"},
		{name: "missing argument leaves the token", key: "greeting", want: "Hallo {name}"},
		{name: "unused argument is ignored", key: "plain", args: []Args{{"name": "Ada"}}, want: "Fertig"},
		{name: "repeated placeholder", key: "twice", args: []Args{{"a": "x"}}, want: "x und x"},
		{
			name: "a value is not expanded again",
			key:  "pair",
			args: []Args{{"a": "{b}", "b": "B"}},
			want: "{b} B",
		},
		{
			name: "several Args merge, the later one wins",
			key:  "pair",
			args: []Args{{"a": "1", "b": "2"}, {"b": "3"}},
			want: "1 3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, c.T(tt.key, tt.args...))
		})
	}
}

func TestLoad(t *testing.T) {
	require.Equal(t, EN, Load(EN).Lang())
	require.Equal(t, DE, Load(DE).Lang())

	// Nothing but Detect produces a Lang, but the type is a string, so
	// anyone can write one: it degrades to English rather than to a catalog
	// with nothing in it.
	require.Equal(t, EN, Load(Lang("fr")).Lang())
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		env        map[string]string
		system     string
		want       Lang
	}{
		{name: "configured de wins over LANG", configured: "de", env: map[string]string{"LANG": "en_US.UTF-8"}, want: DE},
		{name: "configured en wins over LANG", configured: "en", env: map[string]string{"LANG": "de_DE.UTF-8"}, want: EN},
		{name: "configured en wins over the system language", configured: "en", system: "de-DE", want: EN},
		{name: "LC_ALL de", env: map[string]string{"LC_ALL": "de_DE.UTF-8"}, want: DE},
		{name: "the first variable that is set decides", env: map[string]string{"LC_ALL": "C", "LANG": "de_DE"}, want: EN},
		{name: "LC_MESSAGES decides before LANG", env: map[string]string{"LC_MESSAGES": "en_GB", "LANG": "de_DE"}, want: EN},
		{name: "only LC_MESSAGES", env: map[string]string{"LC_MESSAGES": "de_AT"}, want: DE},
		{name: "only LANG", env: map[string]string{"LANG": "de_CH.UTF-8"}, want: DE},
		{name: "an empty variable counts as unset", env: map[string]string{"LC_ALL": "", "LANG": "de_DE"}, want: DE},
		{name: "the prefix is matched case-insensitively", env: map[string]string{"LANG": "DE_de"}, want: DE},
		{name: "another language is English", env: map[string]string{"LANG": "fr_FR.UTF-8"}, want: EN},
		{name: "nothing set, system German", system: "de-DE", want: DE},
		{name: "nothing set, system English", system: "en-US", want: EN},
		{name: "nothing set, no system language", system: "", want: EN},
		{name: "configured fr is ignored", configured: "fr", env: map[string]string{"LANG": "de_DE"}, want: DE},
		{name: "configured value is not matched case-insensitively", configured: "DE", env: map[string]string{"LANG": "en_US"}, want: EN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(name string) string { return tt.env[name] }
			system := func() string { return tt.system }

			require.Equal(t, tt.want, Detect(tt.configured, getenv, system))
		})
	}
}

// The system language is a real API call on Windows, so Detect must not make
// it when the environment already answered: Git Bash sets LANG on Windows,
// and that is the person's explicit choice.
func TestDetectOnlyAsksTheSystemWhenTheEnvironmentIsSilent(t *testing.T) {
	asked := false
	system := func() string {
		asked = true
		return "de-DE"
	}

	got := Detect("", func(name string) string {
		if name == "LANG" {
			return "C"
		}
		return ""
	}, system)

	require.Equal(t, EN, got)
	require.False(t, asked, "an answering LANG must keep Detect from asking the system")

	Detect("de", func(string) string { return "" }, system)
	require.False(t, asked, "a configured language must keep Detect from asking the system")
}
