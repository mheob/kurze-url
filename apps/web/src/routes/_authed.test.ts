import { isNotFound } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import { requireTeamId } from './_authed';

const memberships = [{ team_id: 'a', name: 'Verein A', role: 'owner', slug: 'verein-a' }];

/**
 * Captures whatever `fn` throws instead of asserting inside a try/catch:
 * vitest's `no-conditional-expect` is error-level, and an `expect` call
 * inside a `catch` block only runs when something was actually thrown — a
 * `fn` that throws nothing would silently skip the assertion and the test
 * would pass for the wrong reason. Asserting on this function's return
 * value, unconditionally, is what keeps the "did it throw at all" question
 * and the "what did it throw" question both covered.
 */
function thrown(fn: () => void): unknown {
	try {
		fn();
		return undefined;
	} catch (error) {
		return error;
	}
}

describe('requireTeamId', () => {
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
		expect(isNotFound(thrown(() => requireTeamId(memberships, 'verein-b')))).toBe(true);
	});
});
