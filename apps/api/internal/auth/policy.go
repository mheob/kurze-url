package auth

import (
	_ "embed"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// The policy's rejection reasons. Each Error() string is the token the API
// puts into huma.ErrorDetail.Value and apps/web keys its message off, so
// these sentinels are a wire contract, not internal prose. policy_test.go
// pins the strings.
var (
	ErrPasswordTooShort      = errors.New("too_short")
	ErrPasswordTooLong       = errors.New("too_long")
	ErrPasswordTooRepetitive = errors.New("too_repetitive")
	ErrPasswordFromContext   = errors.New("derived_from_context")
	ErrPasswordTooCommon     = errors.New("too_common")
)

// The policy's numbers. Length is deliberately enforced here rather than as
// minLength/maxLength on the Huma schema: Huma would reject an out-of-range
// value with its own error shape, and two of the five reasons would be
// unreachable over HTTP while still existing here.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 128

	// minDistinctRunes is what stops "!!!!!!!!" and "abababab", both of which
	// clear the length rule. NIST SP 800-63B permits blocking repetitive
	// characters; this is not a character-class rule, and there are none.
	minDistinctRunes = 4

	// minContextToken drops context fragments too short to judge a password
	// by. "sv" or a two-letter country label would reject nearly everything a
	// person could type.
	minContextToken = 4

	// minNormalizedForContext skips the context loop for a password that
	// normalizes to almost nothing — punctuation, which minDistinctRunes has
	// already judged. Comparing an empty string against tokens matches every
	// token. It does not skip the common-list check below: that comparison
	// is equality, not containment, so a short normalized password is not a
	// short comparison — it is simply unlikely to match.
	minNormalizedForContext = 3
)

// PolicyContext carries the values a link password must not be derived from.
// Link passwords are shared out of band with a group, so the failure that
// actually happens is not weakness in the abstract but predictability from
// context: "Sommerfest26" is the first thing anyone who has seen the link
// would guess.
type PolicyContext struct {
	LinkSlug       string
	DestinationURL string
	TeamName       string
	TeamSlug       string
}

//go:embed common-passwords.txt
var commonPasswordSource string

var commonPasswords = loadCommonPasswords(commonPasswordSource)

func loadCommonPasswords(source string) map[string]struct{} {
	set := make(map[string]struct{})
	for line := range strings.SplitSeq(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if normalized := normalizeForPolicy(line); normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	return set
}

// ValidatePassword applies the policy in a fixed order — length, repetition,
// context, then the common list — so the reason a caller sees for a
// password that trips several rules does not depend on anything unwritten.
// Context runs before the common list on purpose: a word that is both a
// context fixture and a common password (e.g. a Verein's own event name)
// should be reported as "derived_from_context", the more specific and more
// actionable reason, rather than the generic "too_common". Reversing the
// order would also mean any word used to test the context rules could never
// appear in the common list, and vice versa — a constraint nobody would
// remember to maintain.
func ValidatePassword(plain string, ctx PolicyContext) error {
	runes := []rune(plain)
	switch {
	case len(runes) < MinPasswordLength:
		return ErrPasswordTooShort
	case len(runes) > MaxPasswordLength:
		return ErrPasswordTooLong
	}

	// Counted over the raw runes, not the normalized form: normalizing first
	// would collapse "!!!!a!!!!" to a single character and fail a password
	// that is merely odd.
	if distinctRunes(runes) < minDistinctRunes {
		return ErrPasswordTooRepetitive
	}

	normalized := normalizeForPolicy(plain)

	// A normalized password too short to judge against context tokens is
	// not too short to look up in the common list: that lookup is equality,
	// not containment, so it isn't sensitive to length the way the
	// containment check is. Skip the context loop, not the whole function.
	if len(normalized) >= minNormalizedForContext {
		for _, token := range contextTokens(ctx) {
			// Both directions: "sommerfest" is contained by the slug
			// "sommerfest-2026", and "svgruenwaldsommerfest" contains the
			// team slug "sv-gruenwald".
			if strings.Contains(normalized, token) || strings.Contains(token, normalized) {
				return ErrPasswordFromContext
			}
		}
	}

	if _, common := commonPasswords[normalized]; common {
		return ErrPasswordTooCommon
	}
	return nil
}

func distinctRunes(runes []rune) int {
	seen := make(map[rune]struct{}, len(runes))
	for _, r := range runes {
		seen[r] = struct{}{}
	}
	return len(seen)
}

// normalizeForPolicy folds a string to the form the context and common-list
// rules compare on: German characters transliterated, lower case, everything
// outside [a-z0-9] dropped.
//
// The transliteration is load-bearing rather than decorative. Without it a
// team named "SV Grünwald" does not catch the password "Gruenwald2026", which
// is exactly the password that team will choose. This is the third
// implementation of the same transliteration in this repository — the others
// are suggestTeamSlug in apps/web/src/lib/team-slug.ts and the team-slug
// backfill migration — and they share no code.
func normalizeForPolicy(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'ä':
			b.WriteString("ae")
		case 'ö':
			b.WriteString("oe")
		case 'ü':
			b.WriteString("ue")
		case 'ß':
			b.WriteString("ss")
		default:
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// contextTokens is every normalized string the password must not contain or
// be contained by: each source's whole value plus its parts, split on the
// separators a name or a slug uses.
func contextTokens(ctx PolicyContext) []string {
	sources := []string{ctx.LinkSlug, ctx.TeamName, ctx.TeamSlug}
	if label := destinationLabel(ctx.DestinationURL); label != "" {
		sources = append(sources, label)
	}

	var tokens []string
	for _, source := range sources {
		candidates := append([]string{source}, strings.FieldsFunc(source, isTokenSeparator)...)
		for _, candidate := range candidates {
			if normalized := normalizeForPolicy(candidate); len(normalized) >= minContextToken {
				tokens = append(tokens, normalized)
			}
		}
	}
	return tokens
}

func isTokenSeparator(r rune) bool {
	return r == '-' || r == '.' || r == '_' || unicode.IsSpace(r)
}

// destinationLabel is the destination's hostname with a leading "www." and
// its last label removed, so https://www.sv-gruenwald.de/verein contributes
// "sv-gruenwald" rather than "de". No public-suffix list: being slightly
// over-inclusive costs a rejected password, and a new dependency would cost
// more. A URL that will not parse contributes nothing — destination.Validate
// has already run by the time a password reaches here.
func destinationLabel(destination string) string {
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	labels := strings.Split(strings.TrimPrefix(parsed.Hostname(), "www."), ".")
	if len(labels) > 1 {
		labels = labels[:len(labels)-1]
	}
	return strings.Join(labels, ".")
}
