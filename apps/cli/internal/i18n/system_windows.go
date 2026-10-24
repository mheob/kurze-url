//go:build windows

package i18n

import "golang.org/x/sys/windows"

// SystemLanguage returns the user's preferred UI language as a BCP 47 name
// such as "de-DE", or "" when Windows cannot say.
//
// Windows has no LANG, and the user's UI language is the closest thing to
// the locale the person chose. The first entry is the one Windows itself
// prefers. A failed lookup is not worth reporting: the CLI falls back to
// English, which is where it would end up with no signal at all.
func SystemLanguage() string {
	languages, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(languages) == 0 {
		return ""
	}
	return languages[0]
}
