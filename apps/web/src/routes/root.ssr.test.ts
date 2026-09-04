import { describe, expect, it } from 'vitest';

import { readLanguage, readTheme } from '../lib/preferences';

/**
 * The full server render is exercised end to end in Task 11. This asserts the
 * decision that feeds it: what the root route derives from a cookie header.
 */
describe('root document preferences', () => {
	it('derives German and dark from the request cookies', () => {
		const cookie = 'lang=de; theme=dark';
		expect(readLanguage(cookie)).toBe('de');
		expect(readTheme(cookie)).toBe('dark');
	});

	it('derives the defaults from no cookies at all', () => {
		expect(readLanguage(undefined)).toBe('en');
		expect(readTheme(undefined)).toBe('light');
	});
});
