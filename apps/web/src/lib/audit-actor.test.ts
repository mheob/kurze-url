import { describe, expect, it } from 'vitest';

import { resolveActor } from './audit-actor.ts';

const MEMBERS = new Map([['user-a', 'anna@example.org']]);

describe(resolveActor, () => {
	it('names a current member by their address', () => {
		expect(resolveActor('user-a', MEMBERS, 'link.updated')).toStrictEqual({
			email: 'anna@example.org',
			kind: 'member',
		});
	});

	it('reports an id that is not in the team as a former member', () => {
		// Removing someone from a team deletes the membership and keeps every
		// entry they wrote, so this is the ordinary case rather than an error.
		expect(resolveActor('user-gone', MEMBERS, 'link.updated')).toStrictEqual({
			kind: 'formerMember',
		});
	});

	it('reports a missing id as a deleted account', () => {
		// `audit_log.actor_user_id` is `on delete set null`, so null means the
		// person's account is gone — a different fact from having left this
		// team, and conflating them would tell a board someone left when they
		// did not.
		expect(resolveActor(undefined, MEMBERS, 'link.updated')).toStrictEqual({
			kind: 'deletedAccount',
		});
	});

	it('names Google Safe Browsing for the two actions only the scanner writes', () => {
		// Also a null id, for a different reason: nobody's account wrote these.
		// The API refuses a null actor on every other action, which is what
		// makes the action enough to tell the two apart.
		expect(resolveActor(undefined, MEMBERS, 'link.flagged')).toStrictEqual({
			kind: 'safeBrowsing',
		});
		expect(resolveActor(undefined, MEMBERS, 'link.unflagged')).toStrictEqual({
			kind: 'safeBrowsing',
		});
	});

	it('does not credit the scanner with an entry a member wrote', () => {
		// The action alone must not decide it: a present id is always a person,
		// whatever the action says.
		expect(resolveActor('user-a', MEMBERS, 'link.flagged')).toStrictEqual({
			email: 'anna@example.org',
			kind: 'member',
		});
	});
});
