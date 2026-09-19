import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { AuditEntryTable } from './audit-entry-table';

const meta = {
	args: {
		entries: [
			{
				action: 'link.updated',
				actor_user_id: 'user-a',
				created_at: '2026-03-14T09:30:00.000Z',
				entity_id: 'link-1',
				entity_type: 'link',
				id: 4,
				metadata: {
					changed: ['slug', 'destination_url'],
					destination_url: 'https://sv-gruenwald.example/sommerfest',
					slug: 'sommerfest',
				},
			},
			{
				action: 'team_member.role_changed',
				actor_user_id: 'user-gone',
				created_at: '2026-03-10T08:00:00.000Z',
				entity_id: 'member-2',
				entity_type: 'team_member',
				id: 3,
				metadata: { role: 'editor' },
			},
			{
				action: 'domain.verified',
				created_at: '2026-03-01T12:00:00.000Z',
				entity_id: 'domain-1',
				entity_type: 'domain',
				id: 2,
				metadata: {},
			},
			{
				action: 'link.created',
				actor_user_id: 'user-a',
				created_at: '2026-02-20T16:45:00.000Z',
				entity_id: 'link-2',
				entity_type: 'link',
				id: 1,
				metadata: {},
			},
		],
		// user-a is a current member; the second entry's actor id ('user-gone')
		// deliberately has no entry here, so it renders as a former member —
		// and the third entry carries no actor id at all, a deleted account.
		language: 'en',
		membersById: new Map([['user-a', 'anna@example.org']]),
	},
	component: AuditEntryTable,
	title: 'Links/AuditEntryTable',
} satisfies Meta<typeof AuditEntryTable>;

export default meta;

/** Four entries, newest first, covering all three actor states and one action with rich metadata. */
export const Default: StoryObj<typeof meta> = {};

export const German: StoryObj<typeof meta> = {
	args: { language: 'de' },
};
