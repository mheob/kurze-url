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

// TestValidatePasswordChecksTheCommonListBeforeTheContext fixes the reported
// reason for a password that trips both rules, so the frontend's message does
// not depend on evaluation order nobody wrote down.
func TestValidatePasswordChecksTheCommonListBeforeTheContext(t *testing.T) {
	ctx := gruenwald
	ctx.TeamName = "Passwort e.V."
	require.ErrorIs(t, auth.ValidatePassword("passwort", ctx), auth.ErrPasswordTooCommon)
}
