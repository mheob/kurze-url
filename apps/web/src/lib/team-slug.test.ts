import { describe, expect, it } from 'vitest';

import { suggestTeamSlug } from './team-slug';

describe('suggestTeamSlug', () => {
	it('transliterates German umlauts rather than dropping them', () => {
		expect(suggestTeamSlug('Sportverein Grünwald')).toBe('sportverein-gruenwald');
		expect(suggestTeamSlug('Schützenverein Höchstädt')).toBe('schuetzenverein-hoechstaedt');
		expect(suggestTeamSlug('Fußballclub')).toBe('fussballclub');
	});

	it('drops a trailing legal form', () => {
		expect(suggestTeamSlug('Sportverein Grünwald e.V.')).toBe('sportverein-gruenwald');
		expect(suggestTeamSlug('Turnverein 1899 e. V.')).toBe('turnverein-1899');
	});

	it('spells out an ampersand, because German reads it as a word', () => {
		expect(suggestTeamSlug('Sport & Spiel')).toBe('sport-und-spiel');
	});

	it('collapses punctuation and whitespace into single hyphens', () => {
		expect(suggestTeamSlug('  TSV   Ober-/Unterdorf  ')).toBe('tsv-ober-unterdorf');
	});

	/**
	 * The truncation must not leave the trailing hyphen a cut through a word
	 * produces: the format the API enforces forbids one, so a suggestion that
	 * ends in `-` would be refused by the server the moment it is submitted
	 * unchanged.
	 */
	it('truncates to the 40-character limit without a trailing hyphen', () => {
		const suggestion = suggestTeamSlug('Verein zur Foerderung des langen Namens im Dorfe');
		expect(suggestion.length).toBeLessThanOrEqual(40);
		expect(suggestion).not.toMatch(/-$/);
	});

	it('returns an empty string when nothing usable is left', () => {
		expect(suggestTeamSlug('!!!')).toBe('');
	});
});
