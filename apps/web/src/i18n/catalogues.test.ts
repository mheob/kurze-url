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
 */
function keysOf(value: Record<string, unknown>, prefix = ''): string[] {
	return Object.entries(value).flatMap(([key, child]) =>
		isRecord(child) ? keysOf(child, `${prefix}${key}.`) : [`${prefix}${key}`],
	);
}

/** Module scope, not inline in the test: it captures nothing from the closure it would sit in. */
function flatten(value: Record<string, unknown>, prefix = ''): [string, string][] {
	return Object.entries(value).flatMap(([key, child]) =>
		isRecord(child) ? flatten(child, `${prefix}${key}.`) : [[`${prefix}${key}`, String(child)]],
	);
}

describe('translation catalogues', () => {
	it('have identical key sets', () => {
		// A Set comparison, not a sorted-array one: key order carries no meaning
		// here, and `Array#sort`/`toSorted` are a mutation footgun / an ES2023
		// method this project's `lib` target doesn't have, respectively.
		expect(new Set(keysOf(de))).toEqual(new Set(keysOf(en)));
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
		]);
		const english = new Map(flatten(en));
		for (const [key, german] of flatten(de)) {
			if (identicalByDesign.has(key)) continue;
			expect(german, `${key} is identical in both languages`).not.toBe(english.get(key));
		}
	});
});
