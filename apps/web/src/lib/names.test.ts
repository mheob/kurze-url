import { describe, expect, it } from 'vitest';

import { nameFailureOf, normalizeName, parseUuidSearch } from './names';

const ID = '0b7c1f6e-2f4a-4f7e-9a53-8a0e1d2c3b4a';

describe(normalizeName, () => {
	it('trims and accepts 1 to 60 characters', () => {
		expect(normalizeName('  Sommerfest  ')).toBe('Sommerfest');
		expect(normalizeName('ä'.repeat(60))).toBe('ä'.repeat(60));
	});

	it('refuses blank and over-long names', () => {
		expect(normalizeName('   ')).toBeUndefined();
		expect(normalizeName('a'.repeat(61))).toBeUndefined();
	});
});

describe(nameFailureOf, () => {
	it('reads 409 as a taken name and 422 by whether the team is at its cap', () => {
		expect(nameFailureOf({ status: 409 }, false)).toBe('nameTaken');
		expect(nameFailureOf({ status: 422 }, true)).toBe('capReached');
		expect(nameFailureOf({ status: 422 }, false)).toBe('nameInvalid');
	});

	it('falls back to classifyApiError for everything else', () => {
		expect(nameFailureOf({ status: 401 }, false)).toBe('unauthenticated');
		expect(nameFailureOf({ status: 404 }, false)).toBe('notFound');
		expect(nameFailureOf({ status: 429 }, false)).toBe('rateLimited');
		expect(nameFailureOf({ status: 500 }, false)).toBe('unknown');
	});
});

describe(parseUuidSearch, () => {
	it('keeps a UUID, lowercasing it', () => {
		expect(parseUuidSearch(ID)).toBe(ID);
		expect(parseUuidSearch(ID.toUpperCase())).toBe(ID);
	});

	it('drops anything else', () => {
		for (const value of [undefined, '', 'none', 'sommerfest', 42, `${ID}x`])
			expect(parseUuidSearch(value)).toBeUndefined();
	});
});
