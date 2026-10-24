import { isNotFound, isRedirect } from '@tanstack/react-router';
import { describe, expect, it } from 'vite-plus/test';

import { type Me, requireMe, requireTeamId } from './_authed';

const memberships = [{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' }];

/**
 * Captures whatever `fn` throws instead of asserting inside a try/catch:
 * vitest's `no-conditional-expect` is error-level, and an `expect` call
 * inside a `catch` block only runs when something was actually thrown — a
 * `fn` that throws nothing would silently skip the assertion and the test
 * would pass for the wrong reason. Asserting on this function's return
 * value, unconditionally, is what keeps the "did it throw at all" question
 * and the "what did it throw" question both covered.
 *
 * @param fn - The synchronous operation expected to throw.
 * @returns Whatever `fn` threw, or `undefined` if it did not throw.
 */
function thrown(fn: () => void): unknown {
	try {
		fn();
		return undefined;
	} catch (error) {
		return error;
	}
}

describe(requireTeamId, () => {
	it('resolves a slug you belong to to that team id', () => {
		expect(requireTeamId(memberships, 'verein-a')).toBe('a');
	});

	/**
	 * 404, never 403 — unchanged from `assertMembership`, which this replaces.
	 * `internal/authz` answers a non-member with 404 so the API never confirms
	 * that a team exists; a frontend rendering "forbidden" here would leak
	 * exactly what the API withholds. An unknown slug is the same answer as a
	 * team you have left: `isNotFound` is what tells that apart from a generic
	 * thrown error, and from nothing thrown at all.
	 */
	it('throws a not-found, not a generic error, for a slug you do not belong to', () => {
		expect(
			isNotFound(
				thrown(() => {
					requireTeamId(memberships, 'verein-b');
				}),
			),
		).toBe(true);
	});
});

describe(requireMe, () => {
	const me: Me = { email: 'a@example.test', is_maintainer: false, memberships, user_id: 'u1' };

	it('hands the signed-in caller to the routes below', () => {
		expect(requireMe(me)).toStrictEqual({ me });
	});

	/**
	 * `fetchMe` answers `null` when there is no session, on the server, so a session that ended
	 * between two client-side navigations still redirects instead of rendering the error page.
	 */
	it('redirects to /login when there is no session', () => {
		const error = thrown(() => {
			requireMe(null);
		});
		expect(isRedirect(error)).toBe(true);
		expect(error).toHaveProperty('options.to', '/login');
	});
});
