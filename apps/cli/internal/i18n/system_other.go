//go:build !windows

package i18n

// SystemLanguage returns "" off Windows. There the locale lives in the
// environment variables Detect reads first, so there is nothing left to ask
// the system.
func SystemLanguage() string {
	return ""
}
