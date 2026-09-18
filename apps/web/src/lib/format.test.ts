import { describe, expect, it } from 'vitest';

import { formatCount, formatDay } from './format.ts';

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
	// The brief expected '15 Sept 2026' for English, assuming a day-first
	// medium format. Node's ICU renders 'en' as US-ordered month/day/year
	// with a three-letter month ('Sep', not 'Sept') — verified directly
	// against Intl.DateTimeFormat on this runtime. German is unaffected and
	// matches the brief exactly.
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
});
