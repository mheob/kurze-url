import type { Language } from './preferences.ts';

/**
 * Both formatters take the language as an argument rather than reading a
 * global. The app renders on the server and hydrates in the browser, and the
 * two must agree exactly or React reports a hydration mismatch — passing the
 * value that already travels through the preferences cookie is what makes
 * them agree by construction.
 *
 * The formatters are memoised because constructing an Intl formatter is the
 * expensive part and a breakdown card builds one per row otherwise.
 */
const counts = new Map<Language, Intl.NumberFormat>();
const days = new Map<Language, Intl.DateTimeFormat>();

/**
 * @param value - A whole number of clicks or visitors.
 * @param language - The active language.
 * @returns The number with the language's own thousands grouping.
 */
export function formatCount(value: number, language: Language): string {
	let formatter = counts.get(language);
	if (formatter === undefined) {
		formatter = new Intl.NumberFormat(language);
		counts.set(language, formatter);
	}
	return formatter.format(value);
}

/**
 * @param isoDate - A calendar date as YYYY-MM-DD, exactly as the API sends it.
 * @param language - The active language.
 * @returns The date in the language's medium form.
 */
export function formatDay(isoDate: string, language: Language): string {
	let formatter = days.get(language);
	if (formatter === undefined) {
		// timeZone: 'UTC' is load-bearing. `new Date('2026-01-01')` is midnight
		// UTC, and formatting that in a negative offset renders 31 December —
		// every point on the chart would be labelled with the wrong day for
		// anyone west of Greenwich.
		formatter = new Intl.DateTimeFormat(language, {
			day: 'numeric',
			month: 'short',
			timeZone: 'UTC',
			year: 'numeric',
		});
		days.set(language, formatter);
	}
	return formatter.format(new Date(`${isoDate}T00:00:00Z`));
}
