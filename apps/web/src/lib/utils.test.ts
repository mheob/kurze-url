import { describe, expect, it } from 'vitest';

import { cn } from './utils';

/*
 * Every generated `ui/*` component composes its own classes with a caller's
 * through `cn`, so a caller overriding a size, padding or colour depends on
 * the later class *replacing* the earlier one rather than both surviving.
 * The npm package `cn` claims to be a drop-in replacement for
 * clsx + tailwind-merge; this is that claim, checked once, because a
 * concatenating implementation fails silently and only where someone
 * overrides.
 */
describe(cn, () => {
	it('lets a later conflicting utility win', () => {
		expect(cn('size-8', 'size-10')).toBe('size-10');
	});

	it('keeps classes that do not conflict', () => {
		expect(cn('flex', 'items-center')).toBe('flex items-center');
	});

	it('drops falsy values', () => {
		expect(cn('flex', false, undefined, 'gap-2')).toBe('flex gap-2');
	});
});
