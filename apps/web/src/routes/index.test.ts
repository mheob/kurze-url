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
		expect(resolveHomeOutcome(undefined, undefined)).toEqual({ kind: 'marketing' });
	});

	it('redirects a signed-in visitor to the resolved team', () => {
		const me: Me = { email: 'a@example.test', memberships, user_id: 'u1' };
		expect(resolveHomeOutcome(me, 'a')).toEqual({ kind: 'redirect', teamId: 'a' });
	});

	/**
	 * The property Task 7 adds: the redirect target is whatever team id the
	 * loader resolved — via `getCurrentTeamId`, which wraps
	 * `resolveCurrentTeam` around the `team` cookie — not hardcoded to the
	 * first membership. `resolveHomeOutcome` itself no longer picks a
	 * membership at all; it only turns an already-resolved id into an
	 * outcome, so passing a non-first id through unchanged is exactly what
	 * proves that. `resolveCurrentTeam`'s own tests in
	 * `lib/current-team.test.ts` cover the cookie-vs-first-membership
	 * decision, including the "removed from that team" falsification.
	 */
	it('redirects to the remembered team, not necessarily the first membership', () => {
		const me: Me = { email: 'a@example.test', memberships, user_id: 'u1' };
		expect(resolveHomeOutcome(me, 'b')).toEqual({ kind: 'redirect', teamId: 'b' });
	});

	it('shows the no-team outcome for a signed-in visitor with no resolved team', () => {
		const me: Me = { email: 'a@example.test', memberships: [], user_id: 'u1' };
		expect(resolveHomeOutcome(me, undefined)).toEqual({ kind: 'noTeam' });
	});
});
