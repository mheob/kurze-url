import { describe, expect, it } from 'vitest';

import {
	matchingPreset,
	parseStatsSearch,
	presetWindow,
	retentionFloor,
	RETENTION_DAYS,
} from './stats-window.ts';

const TODAY = new Date('2026-09-18T11:30:00Z');

describe(presetWindow, () => {
	it('counts today as one of the days', () => {
		expect(presetWindow(7, TODAY)).toStrictEqual({ from: '2026-09-12', to: '2026-09-18' });
	});

	it('resolves the thirty-day preset', () => {
		expect(presetWindow(30, TODAY)).toStrictEqual({ from: '2026-08-20', to: '2026-09-18' });
	});

	it('resolves the longest preset to exactly the retention window', () => {
		expect(presetWindow(RETENTION_DAYS, TODAY)).toStrictEqual({
			from: retentionFloor(TODAY),
			to: '2026-09-18',
		});
	});
});

describe(retentionFloor, () => {
	// The endpoint's floor is today minus 89, which together with today is 90
	// days. Off by one here would ask for a day the endpoint silently drops.
	it('is eighty-nine days before today', () => {
		expect(retentionFloor(TODAY)).toBe('2026-06-21');
	});
});

describe(parseStatsSearch, () => {
	it('keeps a well-formed pair', () => {
		expect(parseStatsSearch({ from: '2026-09-01', to: '2026-09-18' })).toStrictEqual({
			from: '2026-09-01',
			to: '2026-09-18',
		});
	});

	it('drops a malformed date rather than passing it to the API', () => {
		expect(parseStatsSearch({ from: '01.09.2026', to: '2026-09-18' })).toStrictEqual({
			to: '2026-09-18',
		});
	});

	it('drops a well-shaped impossible date', () => {
		expect(parseStatsSearch({ from: '2026-02-31' })).toStrictEqual({});
	});

	it('drops a non-string value', () => {
		expect(parseStatsSearch({ from: 7, to: null })).toStrictEqual({});
	});

	// Absent means "let the endpoint apply its own default". The page must not
	// compute that default itself, or two systems own one number.
	it('returns an empty object when nothing was supplied', () => {
		expect(parseStatsSearch({})).toStrictEqual({});
	});
});

describe(matchingPreset, () => {
	it('recognises a window a preset would have produced', () => {
		expect(matchingPreset({ from: '2026-09-12', to: '2026-09-18' }, TODAY)).toBe(7);
	});

	it('returns undefined for a hand-picked window', () => {
		expect(matchingPreset({ from: '2026-09-03', to: '2026-09-11' }, TODAY)).toBeUndefined();
	});
});
