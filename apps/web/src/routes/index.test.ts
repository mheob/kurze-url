import { describe, expect, it, vi } from 'vitest';

import { UnauthenticatedError } from '../server/session';
import type { Me } from './_authed';

/**
 * `vi.hoisted` plus a narrowly-typed fake, the same mechanics
 * `login.test.tsx` settled on for mocking a server function: a bare
 * `vi.fn()` referenced from `vi.mock`'s factory happens to work, but
 * `vi.hoisted` is what makes that not an accident of hoisting order.
 */
const mocks = vi.hoisted(() => ({
	fetchMe: vi.fn<() => Promise<Me>>(),
}));

vi.mock('./_authed', () => ({ fetchMe: mocks.fetchMe }));

const { fetchCurrentUser, resolveHomeOutcome } = await import('./index');

const memberships = [
	{ name: 'Verein A', role: 'owner', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', team_id: 'b' },
];

describe('fetchCurrentUser', () => {
	/**
	 * The property the task calls out explicitly: `/` is public, so a
	 * signed-out visitor — `fetchMe` throwing `UnauthenticatedError` — must
	 * resolve to `undefined`, an ordinary value, never propagate as a
	 * rejected loader.
	 */
	it('treats "no session" as the ordinary signed-out case', async () => {
		mocks.fetchMe.mockRejectedValueOnce(new UnauthenticatedError());
		await expect(fetchCurrentUser()).resolves.toBeUndefined();
	});

	it('rethrows any other failure rather than treating it as signed-out', async () => {
		const boom = new Error('boom');
		mocks.fetchMe.mockRejectedValueOnce(boom);
		await expect(fetchCurrentUser()).rejects.toBe(boom);
	});

	it('returns the session when there is one', async () => {
		const me: Me = { email: 'a@example.test', memberships, user_id: 'u1' };
		mocks.fetchMe.mockResolvedValueOnce(me);
		await expect(fetchCurrentUser()).resolves.toEqual(me);
	});
});

describe('resolveHomeOutcome', () => {
	it('shows the marketing shell to a signed-out visitor', () => {
		expect(resolveHomeOutcome(undefined)).toEqual({ kind: 'marketing' });
	});

	it('redirects a signed-in visitor with memberships to the first one', () => {
		const me: Me = { email: 'a@example.test', memberships, user_id: 'u1' };
		expect(resolveHomeOutcome(me)).toEqual({ kind: 'redirect', teamId: 'a' });
	});

	it('shows the no-team outcome for a signed-in visitor with no memberships', () => {
		const me: Me = { email: 'a@example.test', memberships: [], user_id: 'u1' };
		expect(resolveHomeOutcome(me)).toEqual({ kind: 'noTeam' });
	});
});
