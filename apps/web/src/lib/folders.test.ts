import { describe, expect, it } from 'vitest';

import {
	folderFailureOf,
	folderFilterOf,
	folderQueryOf,
	normalizeFolderName,
	parseFolderIdSearch,
	parseFolderSearch,
} from './folders';

const ID = '0b7c1f6e-2f4a-4f7e-9a53-8a0e1d2c3b4a';

describe(parseFolderSearch, () => {
	it('keeps "none" and a UUID, lowercasing the UUID', () => {
		expect(parseFolderSearch('none')).toBe('none');
		expect(parseFolderSearch(ID.toUpperCase())).toBe(ID);
	});

	it('drops anything else', () => {
		for (const value of [undefined, '', 'sommerfest', 42, `${ID}x`])
			expect(parseFolderSearch(value)).toBeUndefined();
	});

	it('parseFolderIdSearch refuses "none"', () => {
		expect(parseFolderIdSearch('none')).toBeUndefined();
		expect(parseFolderIdSearch(ID)).toBe(ID);
	});
});

describe('folderFilterOf and folderQueryOf', () => {
	it('maps the search value to the API parameters', () => {
		expect(folderQueryOf(folderFilterOf(undefined))).toStrictEqual({});
		expect(folderQueryOf(folderFilterOf('none'))).toStrictEqual({ unfiled: true });
		expect(folderQueryOf(folderFilterOf(ID))).toStrictEqual({ folder_id: ID });
	});
});

describe(normalizeFolderName, () => {
	it('trims and accepts 1 to 60 characters', () => {
		expect(normalizeFolderName('  Sommerfest  ')).toBe('Sommerfest');
		expect(normalizeFolderName('ä'.repeat(60))).toBe('ä'.repeat(60));
	});

	it('refuses blank and over-long names', () => {
		expect(normalizeFolderName('   ')).toBeUndefined();
		expect(normalizeFolderName('a'.repeat(61))).toBeUndefined();
	});
});

describe(folderFailureOf, () => {
	it('reads 409 as a taken name and 422 by whether the team is at its cap', () => {
		expect(folderFailureOf({ status: 409 }, false)).toBe('nameTaken');
		expect(folderFailureOf({ status: 422 }, true)).toBe('capReached');
		expect(folderFailureOf({ status: 422 }, false)).toBe('nameInvalid');
	});

	it('falls back to classifyApiError for everything else', () => {
		expect(folderFailureOf({ status: 401 }, false)).toBe('unauthenticated');
		expect(folderFailureOf({ status: 404 }, false)).toBe('notFound');
		expect(folderFailureOf({ status: 429 }, false)).toBe('rateLimited');
		expect(folderFailureOf({ status: 500 }, false)).toBe('unknown');
	});
});
