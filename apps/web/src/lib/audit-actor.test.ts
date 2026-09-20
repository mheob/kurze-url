import { describe, expect, it } from 'vitest';

import { resolveActor } from './audit-actor.ts';

const MEMBERS = new Map([['user-a', 'anna@example.org']]);

describe(resolveActor, () => {
	it('names a current member by their address', () => {
		expect(resolveActor('user-a', MEMBERS)).toStrictEqual({
			email: 'anna@example.org',
			kind: 'member',
		});
	});

	it('reports an id that is not in the team as a former member', () => {
		// Removing someone from a team deletes the membership and keeps every
		// entry they wrote, so this is the ordinary case rather than an error.
		expect(resolveActor('user-gone', MEMBERS)).toStrictEqual({ kind: 'formerMember' });
	});

	it('reports a missing id as a deleted account', () => {
		// `audit_log.actor_user_id` is `on delete set null`, so null means the
		// person's account is gone — a different fact from having left this
		// team, and conflating them would tell a board someone left when they
		// did not.
		expect(resolveActor(undefined, MEMBERS)).toStrictEqual({ kind: 'deletedAccount' });
	});
});
