import { describe, expect, it } from 'vitest';

import { validateLinkPassword } from './link-password';

const gruenwald = {
	destinationUrl: 'https://www.sv-gruenwald.de/verein/sommerfest',
	linkSlug: 'sommerfest-2026',
	teamName: 'SV Grünwald e.V.',
	teamSlug: 'sv-gruenwald',
};

describe('validateLinkPassword', () => {
	it('accepts an unrelated passphrase', () => {
		expect(validateLinkPassword('Kartoffelsalat!7', gruenwald)).toBeNull();
	});

	it.each([
		['Abcdef1', 'too_short'],
		['abababab', 'too_repetitive'],
		['!!!!!!!!', 'too_repetitive'],
		['sommerfest2026', 'derived_from_context'],
		['sommerfest', 'derived_from_context'],
		['Gruenwald2026', 'derived_from_context'],
		['Passwort!', 'too_common'],
	])('rejects %s as %s', (password, reason) => {
		expect(validateLinkPassword(password, gruenwald)).toBe(reason);
	});

	it('rejects a password longer than 128 characters', () => {
		const long = Array.from({ length: 129 }, (_, i) => String.fromCharCode(97 + (i % 26))).join('');
		expect(validateLinkPassword(long, gruenwald)).toBe('too_long');
	});

	// The same limit the Go policy pins, for the same reason: it is the
	// documented floor, not an oversight, and tightening it is a decision.
	it('accepts a weak but compliant password', () => {
		expect(validateLinkPassword('passwort1', gruenwald)).toBeNull();
	});

	// Mirrors policy_test.go's "checks the context before the common list":
	// a word that is both a context fixture and (in the Go corpus) a common
	// password must be reported as derived_from_context, the more specific
	// and actionable reason. The order this pins is the correction task-7
	// applied after the brief was written.
	it('reports the context reason before the common-list reason', () => {
		const context = { ...gruenwald, teamName: 'Passwort e.V.' };
		expect(validateLinkPassword('passwort', context)).toBe('derived_from_context');
	});

	// Mirrors policy_test.go's own fixture for this exact path: six
	// punctuation runes plus "ja" satisfy length and distinctness on their
	// own, but normalize down to "ja" — two characters, under the
	// three-character floor for the context comparison. That must skip the
	// context loop without skipping the common-list lookup, which is
	// equality, not containment, so it isn't sensitive to length the same way.
	it('still checks the common list when normalized is too short for context', () => {
		expect(validateLinkPassword('!@#$%^ja', gruenwald)).toBe('too_common');
	});

	// gruenwald's destinationUrl host ("sv-gruenwald") happens to equal its
	// teamSlug, so the cases above never prove the destination host is wired
	// in as its own context source rather than piggybacking on teamSlug. This
	// context makes every other source too short to contribute (single
	// characters normalize below the four-character token floor), isolating
	// the match to the destination host alone.
	it('derives context from the destination host on its own', () => {
		const context = {
			destinationUrl: 'https://www.langlebig-imkerverein.de/x',
			linkSlug: 'x',
			teamName: 'y',
			teamSlug: 'z',
		};
		expect(validateLinkPassword('imkerverein2026', context)).toBe('derived_from_context');
	});

	// gruenwald's teamSlug ("sv-gruenwald") happens to equal its
	// destinationUrl's host label, so every case above that reaches
	// derived_from_context through gruenwald leaves it unproven that teamSlug
	// is wired in as its own context source, rather than piggybacking on the
	// destination host. This context puts an unrelated host on the
	// destination and makes linkSlug/teamName too short to contribute, so a
	// match can only come from teamSlug.
	it('derives context from the team slug on its own', () => {
		const context = {
			destinationUrl: 'https://example.com/x',
			linkSlug: 'x',
			teamName: 'y',
			teamSlug: 'imkerverein-grossstadt',
		};
		expect(validateLinkPassword('imkervereingrossstadt2026', context)).toBe('derived_from_context');
	});

	// Mirrors policy_test.go's TestValidatePasswordFoldsEveryGermanCharacter.
	// Before this, only ü had a fixture — folded implicitly via "Grünwald" in
	// the shared gruenwald context, to catch "Gruenwald2026" — leaving ä, ö
	// and ß unverified. Each case is judged against its own context so a
	// failure to fold isolates to exactly one character.
	it.each([
		['ä to ae', 'FC Bärental', 'baerental'],
		['ö to oe', 'SV Schönau', 'schoenau'],
		['ü to ue', 'TV Grünberg', 'gruenberg'],
		['ß to ss', 'SC Großstadt', 'grossstadt'],
	])('folds %s', (_name, teamName, password) => {
		const context = { ...gruenwald, teamName };
		expect(validateLinkPassword(password, context)).toBe('derived_from_context');
	});

	// Mirrors policy_test.go's TestValidatePasswordIgnoresContextFragmentsBelowFourChars:
	// "ab" appears as a token source in three different places (a slug's
	// leading segment, a name's initial, a team slug's prefix) and normalizes
	// to only two characters in every one of them, so it must be dropped
	// everywhere rather than rejecting almost any password that starts with
	// it. Without MIN_CONTEXT_TOKEN, "abwesenheit9" would be reported
	// derived_from_context against the "ab-" fragment.
	it('ignores context fragments below four characters', () => {
		const context = {
			destinationUrl: 'https://example.com/',
			linkSlug: 'ab-kartoffelsalat',
			teamName: 'AB Beispielverein',
			teamSlug: 'ab-beispielverein',
		};
		expect(validateLinkPassword('abwesenheit9', context)).toBeNull();
	});
});
