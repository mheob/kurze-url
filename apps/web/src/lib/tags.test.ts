import { describe, expect, it, vi } from 'vitest';

import { remapTagGoneFailure, sameTagSet } from './tags';

/**
 * A fresh `invalidateQueries` spy per call, so one test's call count never
 * leaks into the next test's assertion.
 *
 * @returns A spy standing in for `QueryClient.invalidateQueries`.
 */
function invalidateSpy(): ReturnType<
	typeof vi.fn<(filters: Readonly<{ queryKey: readonly unknown[] }>) => Promise<void>>
> {
	return vi.fn<(filters: Readonly<{ queryKey: readonly unknown[] }>) => Promise<void>>();
}

describe(sameTagSet, () => {
	it('ignores order and spots a difference', () => {
		expect(sameTagSet(['a', 'b'], ['b', 'a'])).toBe(true);
		expect(sameTagSet(['a'], ['a', 'b'])).toBe(false);
		expect(sameTagSet([], [])).toBe(true);
	});

	it('is not fooled by equal lengths with different members', () => {
		expect(sameTagSet(['a', 'b'], ['a', 'c'])).toBe(false);
	});
});

describe(remapTagGoneFailure, () => {
	it('rewrites the tag_ids field message and refetches the tags', () => {
		const invalidateQueries = invalidateSpy();
		const out = remapTagGoneFailure(
			{ fields: { tag_ids: 'no tag x in this team' }, kind: 'fields' },
			{ queryClient: { invalidateQueries }, tagGoneMessage: 'gone', teamId: 'team-a' },
		);
		expect(out).toStrictEqual({ fields: { tag_ids: 'gone' }, kind: 'fields' });
		expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ['tags', 'team-a'] });
	});

	it('keeps the other field messages when it rewrites tag_ids', () => {
		const invalidateQueries = invalidateSpy();
		const out = remapTagGoneFailure(
			{ fields: { destination_url: 'required', tag_ids: 'no tag x in this team' }, kind: 'fields' },
			{ queryClient: { invalidateQueries }, tagGoneMessage: 'gone', teamId: 'team-a' },
		);
		expect(out).toStrictEqual({
			fields: { destination_url: 'required', tag_ids: 'gone' },
			kind: 'fields',
		});
	});

	it('passes every other failure through untouched', () => {
		const invalidateQueries = invalidateSpy();
		const failure = { kind: 'unknown' } as const;
		expect(
			remapTagGoneFailure(failure, {
				queryClient: { invalidateQueries },
				tagGoneMessage: 'gone',
				teamId: 'team-a',
			}),
		).toBe(failure);
		expect(invalidateQueries).not.toHaveBeenCalled();
	});

	it('leaves a fields failure that does not name tag_ids untouched, and does not refetch', () => {
		const invalidateQueries = invalidateSpy();
		const failure = { fields: { destination_url: 'required' }, kind: 'fields' } as const;
		expect(
			remapTagGoneFailure(failure, {
				queryClient: { invalidateQueries },
				tagGoneMessage: 'gone',
				teamId: 'team-a',
			}),
		).toBe(failure);
		expect(invalidateQueries).not.toHaveBeenCalled();
	});
});
