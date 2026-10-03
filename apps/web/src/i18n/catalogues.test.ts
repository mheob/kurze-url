import { describe, expect, it } from 'vitest';

import de from './locales/de.json';
import en from './locales/en.json';

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null;
}

/**
 * Half of the no-hardcoded-string rule. This catches a key added to one
 * catalogue and forgotten in the other; it cannot catch a string with no key
 * at all, which is what the rendered-divergence check in Task 11 is for.
 *
 * @param value - The catalogue (or nested object within it) to walk.
 * @param prefix - The dotted key prefix accumulated so far from enclosing objects.
 * @returns Every leaf key, dotted, e.g. `domains.recordTypeTxt`.
 */
function keysOf(value: Readonly<Record<string, unknown>>, prefix = ''): string[] {
	return Object.entries(value).flatMap(([key, child]: readonly [string, unknown]) =>
		isRecord(child) ? keysOf(child, `${prefix}${key}.`) : [`${prefix}${key}`],
	);
}

/**
 * Module scope, not inline in the test: it captures nothing from the closure it would sit in.
 *
 * @param value - The catalogue (or nested object within it) to walk.
 * @param prefix - The dotted key prefix accumulated so far from enclosing objects.
 * @returns Every leaf key/value pair, the key dotted the same way `keysOf` produces it.
 */
function flatten(value: Readonly<Record<string, unknown>>, prefix = ''): [string, string][] {
	return Object.entries(value).flatMap(([key, child]: readonly [string, unknown]) =>
		isRecord(child) ? flatten(child, `${prefix}${key}.`) : [[`${prefix}${key}`, String(child)]],
	);
}

describe('translation catalogues', () => {
	it('have identical key sets', () => {
		// A Set comparison, not a sorted-array one: key order carries no meaning
		// here, and `Array#sort`/`toSorted` are a mutation footgun / an ES2023
		// method this project's `lib` target doesn't have, respectively.
		expect(new Set(keysOf(de))).toStrictEqual(new Set(keysOf(en)));
	});

	it('are not empty', () => {
		expect(keysOf(en).length).toBeGreaterThan(0);
	});

	it('have no German value identical to its English one', () => {
		// A German catalogue copied from English passes key parity while failing
		// the actual requirement. Proper nouns are the legitimate exception and
		// are listed explicitly, so adding one is a deliberate act.
		// `domains.recordTypeTxt`/`recordTypeCname` are the other kind of
		// exception: DNS protocol constants, not prose — "TXT" and "CNAME" are
		// not translated any more than "301"/"302" are (see
		// `links.redirect301`/`redirect302`, which sidestep this same check by
		// embedding the digits in a longer, language-specific string instead).
		const identicalByDesign = new Set([
			'brand',
			'domains.recordTypeTxt',
			'domains.recordTypeCname',
			// "Browser" is the German word too — an established loanword, not a
			// forgotten translation.
			'stats.browser',
			// "Bot" is the German word too, same reasoning as "Browser" above.
			'stats.dimensionValueBot',
			// "QR" is the initialism in both languages, same reasoning as
			// `domains.recordTypeTxt`/`recordTypeCname` above — a protocol-shaped
			// abbreviation, not prose to translate.
			'stats.dimensionValueQr',
			// A formatting template — two interpolated placeholders and a
			// typographic en dash, no actual words — not prose to translate. It
			// still gets a key rather than a literal `–` baked into the
			// component, so a locale whose date-range convention differs (or an
			// RTL one) has somewhere to change it without touching code.
			'stats.rangeSummary',
			// "Link" is the German word too, same reasoning as `stats.browser`.
			'audit.entityLink',
			// "Domain" is the German word too, same reasoning as `stats.browser`.
			'audit.entityDomain',
			// "Person" is spelled identically in both languages, same reasoning
			// as `stats.browser`.
			'audit.filterActor',
			// "Admin" is the German word too — an established loanword, the same
			// reasoning as `stats.browser`. The other three roles are translated.
			'members.roleAdmin',
			// A typographic en dash, the same reasoning as `stats.rangeSummary`
			// above — not a word to translate, and given its own key rather than
			// a literal in `link-list.tsx` only because `react/jsx-no-literals`
			// is error-level project-wide (see the file's own comment on it).
			'links.folderNoneMark',
			// "Tags" is the plural of an established loanword and is spelled the
			// same in both languages, the same reasoning as `stats.browser`.
			'tags.heading',
			// "Tags" again, as the sidebar entry — the same reasoning as `tags.heading`.
			'nav.tags',
			// "Tags" again, as the link form's picker label — the same reasoning
			// as `tags.heading`.
			'links.tags',
			// "Tags" again, as the link list's column header — the same reasoning
			// as `tags.heading`.
			'links.columnTags',
			// "Tag" is the same word in both languages, the same reasoning as
			// `tags.heading`; this is the list filter's label.
			'links.tagFilter',
			// Only the interpolated name varies, and the label word is "Tag" in both
			// languages, so the template is identical — the same reasoning as
			// `stats.rangeSummary`.
			'links.inTag',
			// "Details" is the German word too, the same reasoning as `audit.entityLink`;
			// this is the link list's read-only counterpart to "Edit".
			'links.details',
		]);
		const english = new Map(flatten(en));
		const toCheck = flatten(de).filter(
			([key]: readonly [string, string]) => !identicalByDesign.has(key),
		);
		for (const [key, german] of toCheck) {
			expect(german, `${key} is identical in both languages`).not.toBe(english.get(key));
		}
	});
});
