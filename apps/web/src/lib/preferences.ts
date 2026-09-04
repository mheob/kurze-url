/**
 * Language and theme share this file because they are one mechanism used
 * twice: a cookie, read on the server during rendering so the first paint is
 * already correct. Neither can live in localStorage — a value the server
 * cannot read forces the first paint to guess, and a wrong guess is the flash
 * of untranslated text or of the wrong theme.
 */

export const LANGUAGE_COOKIE = 'lang';
export const THEME_COOKIE = 'theme';

export const LANGUAGES = ['en', 'de'] as const;
export const THEMES = ['light', 'dark'] as const;

export type Language = (typeof LANGUAGES)[number];
export type Theme = (typeof THEMES)[number];

export const DEFAULT_LANGUAGE: Language = 'en';
export const DEFAULT_THEME: Theme = 'light';

function readCookie(cookieHeader: string | undefined, name: string): string | undefined {
	if (!cookieHeader) return undefined;

	for (const part of cookieHeader.split(';')) {
		const [rawKey, ...rawValue] = part.split('=');
		if (rawKey?.trim() === name) return rawValue.join('=').trim();
	}

	return undefined;
}

function isLanguage(value: string | undefined): value is Language {
	return LANGUAGES.some((language) => language === value);
}

function isTheme(value: string | undefined): value is Theme {
	return THEMES.some((theme) => theme === value);
}

/** Cookies are user-controlled, so an unrecognised value falls back rather than propagating. */
export function readLanguage(cookieHeader: string | undefined): Language {
	const value = readCookie(cookieHeader, LANGUAGE_COOKIE);
	return isLanguage(value) ? value : DEFAULT_LANGUAGE;
}

export function readTheme(cookieHeader: string | undefined): Theme {
	const value = readCookie(cookieHeader, THEME_COOKIE);
	return isTheme(value) ? value : DEFAULT_THEME;
}

/** One year, in seconds. A preference should outlive the session that set it. */
const COOKIE_MAX_AGE = 60 * 60 * 24 * 365;

export function preferenceCookie(name: string, value: string): string {
	return `${name}=${value}; Path=/; Max-Age=${COOKIE_MAX_AGE}; SameSite=Lax`;
}
