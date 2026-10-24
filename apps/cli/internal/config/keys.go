package config

import (
	"slices"
	"strings"
)

// Key names one setting the way `config get|set|unset` spells it. The dots
// are the YAML nesting: "qr.fg" is the `fg` entry inside `qr`.
//
// The same Key names a setting in both of its scopes. `qr.fg` is a global
// default at the top of the file and a team's own value under
// `teams.<slug>`; which of the two is meant is the team argument of Get, Set
// and Unset, not part of the Key.
type Key string

// The keys the config file knows. Anything else in a file is an error, so a
// misspelled `qr.colour` is reported instead of being ignored in silence.
const (
	Language Key = "language"
	Team     Key = "team"
	Folder   Key = "folder"
	Domain   Key = "domain"
	QRFormat Key = "qr.format"
	QRSize   Key = "qr.size"
	QRFG     Key = "qr.fg"
	QRBG     Key = "qr.bg"
)

// allKeys is every Key in the order `config get` lists them. It is the one
// list ParseKey, Keys and the scope checks all read, so a ninth key is added
// in one place.
var allKeys = []Key{Language, Team, Folder, Domain, QRFormat, QRSize, QRFG, QRBG}

// Keys returns every known Key in listing order, as a copy the caller may
// modify. `config get` without a key walks it, so the list of settings the
// file understands is defined here and nowhere else.
func Keys() []Key {
	return slices.Clone(allKeys)
}

// ParseKey turns the text a person typed into a Key. Matching is exact and
// case-sensitive, because the same spelling is what the file uses. An
// unknown name comes back as a *ValidationError with CodeUnknownKey, so the
// command layer translates it like any other config failure.
func ParseKey(s string) (Key, error) {
	k := Key(s)
	if !slices.Contains(allKeys, k) {
		return "", &ValidationError{Code: CodeUnknownKey, Key: s}
	}
	return k, nil
}

// Global reports whether the key may appear at the top level of the file,
// where it applies to every team: language, team and qr.*.
func (k Key) Global() bool {
	switch k {
	case Language, Team, QRFormat, QRSize, QRFG, QRBG:
		return true
	}
	return false
}

// PerTeam reports whether the key may appear under a team's own section:
// folder, domain and qr.*. A folder or a domain is meaningless without the
// team that owns it, and a language or a default team is not a per-team idea.
func (k Key) PerTeam() bool {
	switch k {
	case Folder, Domain, QRFormat, QRSize, QRFG, QRBG:
		return true
	}
	return false
}

// segments returns the YAML path of k in the given scope, from the top of
// the file down, or false when k does not live in that scope. An empty team
// is the global scope.
func segments(k Key, team string) ([]string, bool) {
	own := strings.Split(string(k), ".")
	if team == "" {
		return own, k.Global()
	}
	return append([]string{"teams", team}, own...), k.PerTeam()
}
