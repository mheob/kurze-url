import type { Member } from '@kurze-url/api-client';
import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import type { TeamRole } from '../lib/team-roles';
import { MemberList } from './member-list';

/**
 * Mirrors `member-list.test.tsx`'s own fixture — kept local for the same reason that file's docstring gives.
 *
 * @param overrides - Partial fields to override on the default member fixture.
 * @returns The member fixture.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `Member` is `@kurze-url/api-client`'s generated type, whose properties are not marked readonly; that is generated codegen output, never edited by hand.
function member(overrides: Partial<Member> = {}): Member {
	return {
		created_at: '2026-09-01T08:00:00Z',
		email: 'a@verein.test',
		role: 'editor',
		user_id: 'u1',
		...overrides,
	};
}

const meta = {
	// Shared across every story below: no mutation in flight and no failure unless a story says otherwise.
	args: {
		currentUserId: 'u9',
		failedUserId: null,
		failure: null,
		onRemove: fn<(userId: string) => void>(),
		onRoleChange: fn<(userId: string, role: TeamRole) => void>(),
		pendingUserId: null,
	},
	component: MemberList,
	title: 'Members/MemberList',
} satisfies Meta<typeof MemberList>;

export default meta;

/** A viewer sees who is on the team and nothing they can change — no select, no remove button, on any row. */
export const Viewer: StoryObj<typeof meta> = {
	args: {
		actorRole: 'viewer',
		members: [
			member({ role: 'owner', user_id: 'u1' }),
			member({ email: 'b@verein.test', role: 'admin', user_id: 'u2' }),
			member({ email: 'c@verein.test', role: 'editor', user_id: 'u3' }),
		],
	},
};

/** An admin manages everyone below owner, and gets no control at all over the owner's own row. */
export const Admin: StoryObj<typeof meta> = {
	args: {
		actorRole: 'admin',
		currentUserId: 'u2',
		members: [
			member({ role: 'owner', user_id: 'u1' }),
			member({ email: 'b@verein.test', role: 'admin', user_id: 'u2' }),
			member({ email: 'c@verein.test', role: 'viewer', user_id: 'u3' }),
		],
	},
};

/** Two owners: neither is the team's sole owner, so an owner may manage the other owner's row too. */
export const TwoOwners: StoryObj<typeof meta> = {
	args: {
		actorRole: 'owner',
		currentUserId: 'u1',
		members: [
			member({ role: 'owner', user_id: 'u1' }),
			member({ email: 'b@verein.test', role: 'owner', user_id: 'u2' }),
		],
	},
};

/** A team with a sole owner: that row's select renders disabled with an explanation, rather than vanishing. */
export const SoleOwner: StoryObj<typeof meta> = {
	args: {
		actorRole: 'owner',
		currentUserId: 'u1',
		members: [
			member({ role: 'owner', user_id: 'u1' }),
			member({ email: 'b@verein.test', role: 'editor', user_id: 'u2' }),
		],
	},
};

/**
 * A role change lost the race with someone else's edit: the failure renders
 * against the row it happened on, and that row stays usable — `pendingUserId`
 * is `null` here on purpose, since the mutation has already settled by the
 * time a failure exists to show.
 */
export const RacedFailure: StoryObj<typeof meta> = {
	args: {
		actorRole: 'admin',
		currentUserId: 'u9',
		failedUserId: 'u2',
		failure: 'raced',
		members: [
			member({ role: 'owner', user_id: 'u1' }),
			member({ email: 'b@verein.test', role: 'editor', user_id: 'u2' }),
		],
		pendingUserId: null,
	},
};
