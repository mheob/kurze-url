import { isNotFound } from '@tanstack/react-router';
import { describe, expect, it } from 'vitest';

import type { Me } from '../_authed';
import { assertMaintainer } from './teams.new';

function me(isMaintainer: boolean): Me {
	return {
		email: 'a@example.test',
		is_maintainer: isMaintainer,
		memberships: [],
		user_id: 'u1',
	};
}

/**
 * Same shape as `_authed.test.ts`'s helper, and for the same reason: vitest's
 * `no-conditional-expect` is error-level, and an assertion inside a `catch`
 * silently does not run when nothing was thrown.
 */
function thrown(fn: () => void): unknown {
	try {
		fn();
		return undefined;
	} catch (error) {
		return error;
	}
}

describe('assertMaintainer', () => {
	it('lets a maintainer through', () => {
		expect(() => assertMaintainer(me(true))).not.toThrow();
	});

	/**
	 * `isNotFound`, not a bare `.toThrow()`: swapping `notFound()` for any
	 * other error would leave a weaker assertion green. The distinction is
	 * deliberate — `assertMembership` in the same tree answers 404 rather than
	 * 403, and a second guard next to it answering differently is what later
	 * gets copied into a route where the difference does leak something.
	 */
	it('throws a not-found, not a generic error, for everyone else', () => {
		expect(isNotFound(thrown(() => assertMaintainer(me(false))))).toBe(true);
	});
});
