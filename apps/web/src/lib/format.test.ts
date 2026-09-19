import { describe, expect, it } from 'vitest';

import { formatCount, formatDateTime, formatDay, LOCALE_TAGS } from './format.ts';

describe(formatCount, () => {
	it('groups thousands the English way', () => {
		expect(formatCount(1_234_567, 'en')).toBe('1,234,567');
	});

	it('groups thousands the German way', () => {
		expect(formatCount(1_234_567, 'de')).toBe('1.234.567');
	});

	it('leaves a small number alone in both languages', () => {
		expect(formatCount(7, 'en')).toBe('7');
		expect(formatCount(7, 'de')).toBe('7');
	});

	it('renders zero rather than an empty string', () => {
		expect(formatCount(0, 'en')).toBe('0');
	});
});

describe(formatDay, () => {
	it('renders a day the English way', () => {
		expect(formatDay('2026-09-15', 'en')).toBe('Sep 15, 2026');
	});

	it('renders a day the German way', () => {
		expect(formatDay('2026-09-15', 'de')).toBe('15. Sept. 2026');
	});

	// The API sends a plain calendar date. Parsing it as local time would shift
	// it by a day for anyone west of UTC, which would silently relabel every
	// point on the chart.
	it('does not shift the date across a timezone boundary', () => {
		expect(formatDay('2026-01-01', 'en')).toContain('2026');
		expect(formatDay('2026-01-01', 'en')).toContain('1');
	});

	// This guards the region binding itself, not the date order: on this Node
	// build a bare 'en' and 'en-US' render an identical string ('Sep 15,
	// 2026'), so no output of formatDay can tell them apart any more. Only
	// Intl's own resolvedOptions().locale distinguishes 'en' (unqualified,
	// left to the runtime's default region) from 'en-US' (pinned) — which is
	// precisely the ambiguity LOCALE_TAGS exists to remove. Verified by hand
	// that reverting LOCALE_TAGS.en to a bare 'en' turns this red.
	it('binds English to a region-qualified locale', () => {
		expect(new Intl.DateTimeFormat(LOCALE_TAGS.en).resolvedOptions().locale).toBe('en-US');
	});
});

describe(formatDateTime, () => {
	it('renders an instant the English way', () => {
		expect(formatDateTime('2026-03-14T09:30:00.000Z', 'en')).toBe('Mar 14, 2026, 9:30 AM');
	});

	it('renders an instant the German way', () => {
		expect(formatDateTime('2026-03-14T09:30:00.000Z', 'de')).toBe('14.03.2026, 09:30');
	});

	// Same reasoning as formatDay's own timezone test: the API sends a full
	// instant, and formatting it in anything but UTC would let two readers in
	// different places disagree about when something happened.
	it('does not shift the instant across a timezone boundary', () => {
		expect(formatDateTime('2026-01-01T00:30:00.000Z', 'en')).toContain('Jan 1, 2026');
	});
});
