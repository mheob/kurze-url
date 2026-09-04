import { describe, expect, it } from 'vitest';

import { readLanguage, readTheme, themeClassName } from './preferences';

describe('readLanguage', () => {
	it('reads the language from a cookie header', () => {
		expect(readLanguage('lang=de')).toBe('de');
	});

	it('finds its cookie among others', () => {
		expect(readLanguage('theme=dark; lang=de; other=x')).toBe('de');
	});

	it('defaults to English when no cookie is set', () => {
		expect(readLanguage(undefined)).toBe('en');
		expect(readLanguage('')).toBe('en');
	});

	it('defaults to English for a value it does not support', () => {
		// A cookie is user-controlled input. An unknown value must not reach
		// i18next, which would fall back silently and render untranslated keys.
		expect(readLanguage('lang=fr')).toBe('en');
		expect(readLanguage('lang=<script>')).toBe('en');
	});

	it('falls back to Accept-Language when there is no cookie', () => {
		expect(readLanguage(undefined, 'de-DE,de;q=0.9,en;q=0.8')).toBe('de');
	});

	it('picks the highest-quality supported language, not just the first tag', () => {
		// English listed first but lower priority than German — the *quality*
		// value decides, not list order.
		expect(readLanguage(undefined, 'en;q=0.5,de;q=0.9')).toBe('de');
	});

	it('treats a tag with no explicit q as quality 1', () => {
		expect(readLanguage(undefined, 'en;q=0.1,de')).toBe('de');
	});

	it('skips an unsupported language for the next candidate', () => {
		expect(readLanguage(undefined, 'fr-FR,fr;q=0.9,de;q=0.5')).toBe('de');
	});

	it('defaults to English when Accept-Language has no supported language', () => {
		expect(readLanguage(undefined, 'fr-FR,fr;q=0.9')).toBe('en');
	});

	it('defaults to English for a missing or empty Accept-Language', () => {
		expect(readLanguage(undefined, undefined)).toBe('en');
		expect(readLanguage(undefined, '')).toBe('en');
	});

	it('does not throw and defaults to English on a malformed Accept-Language', () => {
		// User-controlled input, exactly like a cookie: garbage must degrade to
		// the default, never throw, and never reach i18next unrecognised.
		expect(readLanguage(undefined, ',,,')).toBe('en');
		expect(readLanguage(undefined, ';;;')).toBe('en');
		expect(readLanguage(undefined, 'de;q=not-a-number')).toBe('de');
		expect(readLanguage(undefined, '<script>alert(1)</script>;q=1')).toBe('en');
	});

	it('prefers a cookie over Accept-Language when both are present', () => {
		expect(readLanguage('lang=de', 'en-US,en;q=0.9')).toBe('de');
	});
});

describe('readTheme', () => {
	it('reads the theme from a cookie header', () => {
		expect(readTheme('theme=dark')).toBe('dark');
	});

	it('defaults to light when unset or unsupported', () => {
		expect(readTheme(undefined)).toBe('light');
		expect(readTheme('theme=neon')).toBe('light');
	});
});

describe('themeClassName', () => {
	it('maps dark to the dark class', () => {
		expect(themeClassName('dark')).toBe('dark');
	});

	it('maps light to no class', () => {
		expect(themeClassName('light')).toBeUndefined();
	});
});
