import { isNotFound } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import { assertMembership } from './_authed';

const memberships = [{ team_id: 'a', name: 'Verein A', role: 'owner' }];

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

describe('assertMembership', () => {
	it('passes for a team you belong to', () => {
		expect(() => assertMembership(memberships, 'a')).not.toThrow();
	});

	/**
	 * The falsification the brief's own Step 5 gets wrong: it says asserting
	 * only that *something* throws is acceptable, and that swapping
	 * `throw notFound()` for `throw new Error('forbidden')` leaving the test
	 * green is fine to leave alone. It is not, per this task's brief — 404,
	 * never 403. `internal/authz` in the Go API already answers 404 for a
	 * non-member so the API itself never confirms a team exists; a frontend
	 * that rendered "forbidden" here would leak exactly what the API
	 * withholds. `isNotFound` is what tells a not-found apart from a generic
	 * thrown error (and from nothing thrown at all — `isNotFound(undefined)`
	 * is also `false`), so this assertion — not a bare `.toThrow()` — is what
	 * fails when that swap happens. See the falsification note in the task
	 * report for the run that confirms it.
	 */
	it('throws a not-found, not a generic error, for a team you do not belong to', () => {
		expect(isNotFound(thrown(() => assertMembership(memberships, 'b')))).toBe(true);
	});
});
