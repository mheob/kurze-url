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
