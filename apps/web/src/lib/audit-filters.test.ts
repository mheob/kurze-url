import { describe, expect, it } from 'vitest';

import { hasActiveFilters, parseAuditFilters, toQueryRange } from './audit-filters.ts';

describe(parseAuditFilters, () => {
	it('defaults to the first page and no filters', () => {
		expect(parseAuditFilters({})).toStrictEqual({ page: 1 });
	});

	it('keeps a known entity type and drops an unknown one', () => {
		expect(parseAuditFilters({ entityType: 'link' }).entityType).toBe('link');
		// Anything may arrive in a URL. An unknown value must not reach the API
		// as a filter it would refuse with a 422.
		expect(parseAuditFilters({ entityType: 'giraffe' }).entityType).toBeUndefined();
	});

	it('keeps dates only in the shape the API takes', () => {
		expect(parseAuditFilters({ from: '2026-03-01' }).from).toBe('2026-03-01');
		expect(parseAuditFilters({ from: '01.03.2026' }).from).toBeUndefined();
		expect(parseAuditFilters({ to: 'yesterday' }).to).toBeUndefined();
	});

	it('clamps a page below one rather than sending it', () => {
		expect(parseAuditFilters({ page: 0 }).page).toBe(1);
		expect(parseAuditFilters({ page: -4 }).page).toBe(1);
		expect(parseAuditFilters({ page: 2.7 }).page).toBe(1);
		expect(parseAuditFilters({ page: 3 }).page).toBe(3);
	});

	it('passes an actor through as an opaque string', () => {
		// The API validates the UUID and answers 422 itself; re-deciding that
		// here would put a second rule where one already exists.
		expect(parseAuditFilters({ actor: 'not-a-uuid' }).actor).toBe('not-a-uuid');
	});
});

describe(toQueryRange, () => {
	it('sends nothing when no dates are chosen', () => {
		expect(toQueryRange({ page: 1 })).toStrictEqual({});
	});

	it('covers the whole of both chosen days', () => {
		// The bound that matters: `to` as midnight would exclude all but the
		// first instant of the day the reader picked, so choosing "to: today"
		// would hide everything that happened today.
		expect(toQueryRange({ from: '2026-03-01', page: 1, to: '2026-03-31' })).toStrictEqual({
			from: '2026-03-01T00:00:00.000Z',
			to: '2026-03-31T23:59:59.999Z',
		});
	});

	it('sends one bound when only one is chosen', () => {
		expect(toQueryRange({ from: '2026-03-01', page: 1 })).toStrictEqual({
			from: '2026-03-01T00:00:00.000Z',
		});
		expect(toQueryRange({ page: 1, to: '2026-03-31' })).toStrictEqual({
			to: '2026-03-31T23:59:59.999Z',
		});
	});
});

describe(hasActiveFilters, () => {
	it('reports no filters for a bare page', () => {
		expect(hasActiveFilters({ page: 1 })).toBe(false);
	});

	/**
	 * The page is where the reader is, not what they asked to see. Counting it
	 * would put the filter bar's "clear the filters" button on every page after
	 * the first, and would make the page tell a reader who filtered nothing
	 * that no changes match their filters.
	 */
	it('does not count the page as a filter', () => {
		expect(hasActiveFilters({ page: 7 })).toBe(false);
	});

	it('reports a filter for each of the four that exist', () => {
		expect(hasActiveFilters({ actor: 'user-a', page: 1 })).toBe(true);
		expect(hasActiveFilters({ entityType: 'link', page: 1 })).toBe(true);
		expect(hasActiveFilters({ from: '2026-03-01', page: 1 })).toBe(true);
		expect(hasActiveFilters({ page: 1, to: '2026-03-31' })).toBe(true);
	});
});
