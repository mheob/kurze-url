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
});
