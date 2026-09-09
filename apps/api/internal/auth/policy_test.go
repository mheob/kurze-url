package auth_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
)

// gruenwald is the context every case below is judged against: a real-shaped
// Verein, whose name carries an umlaut the policy has to fold before it can
// catch the password that Verein would actually pick.
var gruenwald = auth.PolicyContext{
	LinkSlug:       "sommerfest-2026",
	DestinationURL: "https://www.sv-gruenwald.de/verein/sommerfest",
	TeamName:       "SV Grünwald e.V.",
	TeamSlug:       "sv-gruenwald",
}

func TestValidatePasswordAcceptsAnUnrelatedPassphrase(t *testing.T) {
	require.NoError(t, auth.ValidatePassword("Kartoffelsalat!7", gruenwald))
}

func TestValidatePasswordRejectsByRule(t *testing.T) {
	for name, tc := range map[string]struct {
		password string
		want     error
	}{
		"seven characters":        {"Abcdef1", auth.ErrPasswordTooShort},
		"one hundred twenty nine": {repeatRunes(129), auth.ErrPasswordTooLong},
		"three distinct runes":    {"abababab", auth.ErrPasswordTooRepetitive},
		"only punctuation":        {"!!!!!!!!", auth.ErrPasswordTooRepetitive},
		"the link's own slug":     {"sommerfest2026", auth.ErrPasswordFromContext},
		"contained by the slug":   {"sommerfest", auth.ErrPasswordFromContext},
		"the team slug extended":  {"svgruenwaldsommerfest", auth.ErrPasswordFromContext},
		"the team name folded":    {"Gruenwald2026", auth.ErrPasswordFromContext},
		"the destination host":    {"svgruenwald.de!", auth.ErrPasswordFromContext},
		"a common password":       {"Passwort!", auth.ErrPasswordTooCommon},
		// "ja" is the only common-list entry short enough to normalize to
		// under minNormalizedForContext (3): the six punctuation runes
		// satisfy length and distinctness on their own, so normalizing
		// strips them and leaves exactly "ja". That skips the context loop
		// entirely, but must not skip the common-list lookup — it still
		// reports too_common rather than falling through to nil.
		"shorter than the context threshold, still common": {"!@#$%^ja", auth.ErrPasswordTooCommon},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, auth.ValidatePassword(tc.password, gruenwald), tc.want)
		})
	}
}

// repeatRunes builds a string of n distinct-enough characters, so the only
// rule it can trip is the length ceiling.
func repeatRunes(n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = rune('a' + i%26)
	}
	return string(out)
}

// TestValidatePasswordReasonsAreTheWireTokens pins the one coupling the API
// depends on: the handler puts err.Error() straight into
// huma.ErrorDetail.Value, and apps/web keys its message off that string.
// Rewording one of these sentinels would silently change the wire contract.
func TestValidatePasswordReasonsAreTheWireTokens(t *testing.T) {
	require.Equal(t, "too_short", auth.ErrPasswordTooShort.Error())
	require.Equal(t, "too_long", auth.ErrPasswordTooLong.Error())
	require.Equal(t, "too_repetitive", auth.ErrPasswordTooRepetitive.Error())
	require.Equal(t, "derived_from_context", auth.ErrPasswordFromContext.Error())
	require.Equal(t, "too_common", auth.ErrPasswordTooCommon.Error())
}

// TestValidatePasswordAcceptsAWeakButCompliantPassword records the policy's
// documented limit rather than leaving it to be rediscovered as a bug. The
// spec says so in as many words: the policy raises the floor, the rate limits
// bound the damage. Tightening this needs a decision, not a patch.
func TestValidatePasswordAcceptsAWeakButCompliantPassword(t *testing.T) {
	require.NoError(t, auth.ValidatePassword("passwort1", gruenwald))
}

// TestValidatePasswordChecksTheContextBeforeTheCommonList fixes the reported
// reason for a password that trips both rules: context wins, because it is
// the more specific and more actionable of the two — "derived_from_context"
// tells this particular Verein not to reuse its own team name, where
// "too_common" would only tell them the word is common somewhere. The
// frontend's message must not depend on evaluation order nobody wrote down.
func TestValidatePasswordChecksTheContextBeforeTheCommonList(t *testing.T) {
	ctx := gruenwald
	ctx.TeamName = "Passwort e.V."
	require.ErrorIs(t, auth.ValidatePassword("passwort", ctx), auth.ErrPasswordFromContext)
}

// TestValidatePasswordFoldsEveryGermanCharacter exercises each of the four
// transliterations in normalizeForPolicy individually. Before this, only ü
// had a fixture — folded implicitly via "Grünwald" in the shared gruenwald
// context, to catch "Gruenwald2026" — leaving ä, ö and ß unverified. Each
// case here is judged against a dedicated context so a failure to fold
// isolates to exactly one character.
func TestValidatePasswordFoldsEveryGermanCharacter(t *testing.T) {
	for name, tc := range map[string]struct {
		teamName string
		password string
	}{
		"ä to ae": {"FC Bärental", "baerental"},
		"ö to oe": {"SV Schönau", "schoenau"},
		"ü to ue": {"TV Grünberg", "gruenberg"},
		"ß to ss": {"SC Großstadt", "grossstadt"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := gruenwald
			ctx.TeamName = tc.teamName
			require.ErrorIs(t, auth.ValidatePassword(tc.password, ctx), auth.ErrPasswordFromContext)
		})
	}
}

// TestValidatePasswordIgnoresContextFragmentsBelowFourChars pins
// minContextToken: "ab" appears as a token source in three different
// places below (a slug's leading segment, a name's initial, a team slug's
// prefix) and normalizes to only two characters in every one of them, so it
// is dropped everywhere rather than rejecting almost any password that
// happens to start with it.
func TestValidatePasswordIgnoresContextFragmentsBelowFourChars(t *testing.T) {
	ctx := auth.PolicyContext{
		LinkSlug:       "ab-kartoffelsalat",
		DestinationURL: "https://example.com/",
		TeamName:       "AB Beispielverein",
		TeamSlug:       "ab-beispielverein",
	}
	require.NoError(t, auth.ValidatePassword("abwesenheit9", ctx))
}
