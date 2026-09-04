import { describe, expect, it } from 'vitest';

import { documentTitle } from './index';
import de from './locales/de.json';
import en from './locales/en.json';

/**
 * axe's `document-title` rule only checks the title is non-empty, and the
 * rendered-divergence spec (`e2e/i18n.spec.ts`) scopes to `body :visible` and
 * `[aria-label]`, so it never sees `<head>`. Neither check can catch
 * `documentTitle` reading the wrong catalogue for a given language — this
 * unit test is the only thing that does. Expected values come from the
 * catalogues themselves, not literals, so the title text stays defined in
 * exactly one place.
 */
describe('documentTitle', () => {
	it('returns the German title for de', () => {
		expect(documentTitle('de')).toBe(de.pageTitle);
	});

	it('returns the English title for en', () => {
		expect(documentTitle('en')).toBe(en.pageTitle);
	});
});
