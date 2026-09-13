/*
 * Language and theme share this file because they are one mechanism used
 * twice: a cookie, read on the server during rendering so the first paint is
 * already correct. Neither can live in localStorage — a value the server
 * cannot read forces the first paint to guess, and a wrong guess is the flash
 * of untranslated text or of the wrong theme.
 *
 * A single `*` on purpose, not `/**`: this is a file overview, not a doc
 * comment for `readCookie` below it, and a `/**` block here reads to
 * `jsdoc/require-param`/`require-returns` as an undocumented-but-tagged
 * comment on that function, which it was never meant to be.
 */

function readCookie(cookieHeader: string | undefined, name: string): string | undefined {
	if (cookieHeader === undefined || cookieHeader === '') return undefined;

	for (const part of cookieHeader.split(';')) {
		const [rawKey, ...rawValue] = part.split('=');
		if (rawKey?.trim() === name) return rawValue.join('=').trim();
	}

	return undefined;
}

// LANGUAGES/THEMES stay here, ahead of the exports block below, even though
// that leaves these two `export const`s flagged by import(exports-last):
// isLanguage/isTheme read them by value, and eslint(no-use-before-define)
// checks a variable's textual position regardless of the enclosing
// function's hoisting, so moving these down would trade one lint rule's
// warning for the other. Only these two are pinned here — every other
// export in this file moved cleanly.
/* oxlint-disable import/exports-last -- isLanguage/isTheme below read these by value, and
   eslint(no-use-before-define) checks a variable's textual position regardless of the enclosing
   function's hoisting; moving these two exports to the bottom would only trade this warning for
   that one. */
export const LANGUAGES = ['en', 'de'] as const;
export const THEMES = ['light', 'dark'] as const;
/* oxlint-enable import/exports-last */

function isLanguage(value: string | undefined): value is Language {
	return LANGUAGES.some((language) => language === value);
}

function isTheme(value: string | undefined): value is Theme {
	return THEMES.some((theme) => theme === value);
}

/**
 * The primary subtag of a language tag: `de` from `de-DE`, `en` from `en`.
 * `noUncheckedIndexedAccess` makes `tag.split('-')[0]` read as possibly
 * `undefined` even though a non-empty string always has one; slicing on the
 * first `-` instead keeps the return type a plain `string`.
 *
 * @param tag - A language tag, e.g. `de-DE`.
 * @returns The primary subtag, e.g. `de`.
 */
const NOT_FOUND = -1;

function primarySubtag(tag: string): string {
	const dashIndex = tag.indexOf('-');
	return dashIndex === NOT_FOUND ? tag : tag.slice(0, dashIndex);
}

function acceptLanguageQuality(entry: string): number {
	const qParam = entry
		.split(';')
		.slice(1)
		.find((param) => param.trim().startsWith('q='));
	if (qParam === undefined) return 1;

	// A malformed `q` (e.g. `q=not-a-number`) degrades to 0 rather than
	// propagating NaN into the comparison below — the header is as
	// user-controlled as a cookie, so garbage should just lose priority, not
	// throw or corrupt the comparison.
	const quality = Number(qParam.trim().slice(2));
	return Number.isFinite(quality) ? quality : 0;
}

/**
 * The spec treats `Accept-Language` as the initial guess before a cookie
 * exists, not an ongoing source of truth — this is only ever consulted when
 * `readLanguage` finds no cookie. The header is as user-controlled as a
 * cookie value: a malformed entry is skipped rather than thrown on, and an
 * unsupported language anywhere in the list is skipped in favour of the next
 * one, never propagated to i18next unrecognised.
 *
 * A single pass tracking the best match, rather than building and sorting a
 * candidate array: oxlint's `unicorn/no-array-sort` requires `.toSorted()`
 * over `.sort()` project-wide, but `.toSorted()` is ES2023 — outside
 * apps/web/tsconfig.json's own `lib` (ES2022), the same constraint
 * catalogues.test.ts sidesteps by avoiding a sorted-array comparison
 * entirely. Root tsconfig.json's `lib` (ES2025) does cover ES2023, and that
 * is the config `pnpm typecheck` runs against — but oxlint's own type-aware
 * rules resolve apps/web files against apps/web/tsconfig.json, so `.toSorted()`
 * still fails `pnpm lint` even though `pnpm typecheck` accepts it. Avoiding
 * the sort keeps both gates satisfied by construction, and ties naturally go
 * to whichever supported tag appears first — a reasonable, standard-adjacent
 * tie-break given `Accept-Language` doesn't mandate one.
 *
 * @param header - The request's `Accept-Language` header value, if any.
 * @returns The highest-quality supported language, or `undefined` if none of the header's
 * tags are supported.
 */
function parseAcceptLanguage(header: string | undefined): Language | undefined {
	if (header === undefined || header === '') return undefined;

	let best: { language: Language; quality: number } | undefined = undefined;

	for (const entry of header.split(',')) {
		const tag = entry.split(';')[0]?.trim().toLowerCase();
		const primary = tag ? primarySubtag(tag) : undefined;

		if (primary !== undefined && isLanguage(primary)) {
			const quality = acceptLanguageQuality(entry);
			if (!best || quality > best.quality) best = { language: primary, quality };
		}
	}

	return best?.language;
}

const SECONDS_PER_MINUTE = 60;
const MINUTES_PER_HOUR = 60;
const HOURS_PER_DAY = 24;
const DAYS_PER_YEAR = 365;

/** One year, in seconds. A preference should outlive the session that set it. */
const COOKIE_MAX_AGE = SECONDS_PER_MINUTE * MINUTES_PER_HOUR * HOURS_PER_DAY * DAYS_PER_YEAR;

export const LANGUAGE_COOKIE = 'lang';
export const THEME_COOKIE = 'theme';

export type Language = (typeof LANGUAGES)[number];
export type Theme = (typeof THEMES)[number];

export const DEFAULT_LANGUAGE: Language = 'en';
export const DEFAULT_THEME: Theme = 'light';

/**
 * Cookies are user-controlled, so an unrecognised value falls back rather
 * than propagating. With no cookie at all, `acceptLanguageHeader` — the
 * request's `Accept-Language` — supplies a one-time initial guess; still
 * falls back to `DEFAULT_LANGUAGE` if that is absent, unsupported, or
 * malformed.
 *
 * @param cookieHeader - The request's raw `Cookie` header, if any.
 * @param acceptLanguageHeader - The request's `Accept-Language` header, consulted only when
 * `cookieHeader` carries no recognized language.
 * @returns The language to render with.
 */
export function readLanguage(
	cookieHeader: string | undefined,
	acceptLanguageHeader?: string,
): Language {
	const value = readCookie(cookieHeader, LANGUAGE_COOKIE);
	if (isLanguage(value)) return value;

	return parseAcceptLanguage(acceptLanguageHeader) ?? DEFAULT_LANGUAGE;
}

export function readTheme(cookieHeader: string | undefined): Theme {
	const value = readCookie(cookieHeader, THEME_COOKIE);
	return isTheme(value) ? value : DEFAULT_THEME;
}

/**
 * Named on purpose: an inline `theme === 'dark' ? 'dark' : undefined` ternary
 * in JSX is invisible to every test, so an inverted comparison would silently
 * flip the theme with nothing to catch it. Pulling the mapping out here makes
 * it a unit the root route's test suite can assert on directly.
 *
 * @param theme - The current theme preference.
 * @returns The class name to apply for `theme`, or `undefined` for the light theme.
 */
export function themeClassName(theme: Theme): string | undefined {
	return theme === 'dark' ? 'dark' : undefined;
}

export function preferenceCookie(name: string, value: string): string {
	return `${name}=${value}; Path=/; Max-Age=${COOKIE_MAX_AGE}; SameSite=Lax`;
}
