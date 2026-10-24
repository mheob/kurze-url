package i18n

import "strings"

// Detect picks the language to speak.
//
// A configured value of "en" or "de" wins, because it is the person's own
// choice in the config file. Anything else there is ignored, so a value the
// config's validation would already have refused cannot silence detection.
// Otherwise the first of LC_ALL, LC_MESSAGES and LANG that is non-empty
// decides on its own, the way POSIX resolves them: LC_ALL=C with LANG=de_DE
// is English, because the first one that is set overrides the rest. Only
// when all three are empty is system asked, and when that says nothing too
// the answer is English.
//
// The environment comes before the system on Windows as well, because Git
// Bash and WSL-style setups set LANG there deliberately.
//
// getenv and system are parameters so a test supplies a map and a string,
// and never reads the process's environment or the machine's locale.
func Detect(configured string, getenv func(string) string, system func() string) Lang {
	if l := Lang(configured); l == EN || l == DE {
		return l
	}
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := getenv(name); value != "" {
			return fromLocale(value)
		}
	}
	if value := system(); value != "" {
		return fromLocale(value)
	}
	return EN
}

// fromLocale maps a locale name such as de_DE.UTF-8, de-DE or C to a
// language: German when it starts with "de" in any case, English otherwise.
func fromLocale(locale string) Lang {
	if strings.HasPrefix(strings.ToLower(locale), "de") {
		return DE
	}
	return EN
}
