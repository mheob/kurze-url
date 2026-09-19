import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { userEvent, within } from 'storybook/test';

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
				// The real shape a PATCH writes (`apps/api/internal/api/links.go`):
				// one `{from, to}` object per changed field, never a bare string —
				// the brief's own test fixture uses a flat shape the API cannot
				// actually emit, which is what the fix-round nested-object test in
				// `audit-entry-table.test.tsx` guards against regressing.
				metadata: {
					changed: ['slug', 'destination_url'],
					destination_url: {
						from: 'https://sv-gruenwald.example/sommerfest',
						to: 'https://sv-gruenwald.example/sommerfest-2026',
					},
					slug: { from: 'sommerfest', to: 'sommerfest-2026' },
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
	// Team-scoped, not link-scoped — `Domains/DomainList` and `Shell/*` are
	// this repo's precedent for a per-feature namespace rather than filing
	// every table under `Links/`.
	title: 'Audit/AuditEntryTable',
} satisfies Meta<typeof AuditEntryTable>;

export default meta;

/**
 * Four entries, newest first, covering all three actor states, one action
 * with rich (nested-object and array) metadata, and two with none at all —
 * the third and fourth entries render no details toggle, by design.
 */
export const Default: StoryObj<typeof meta> = {};

export const German: StoryObj<typeof meta> = {
	args: { language: 'de' },
};

/**
 * Opens the `link.updated` entry's disclosure so its `<dl>` — including the
 * nested `{from, to}` object the fix round added — is part of the DOM
 * `test:storybook` scans with axe, not only the collapsed state every other
 * story leaves behind. `stat-summary.tsx` records why this matters: its own
 * `<dl>` violation was caught by exactly this mechanism.
 *
 * Matched by its action label ("Link changed"), not by list position: two
 * rows have a details button, and `getAllByRole(...)[0]` would be
 * `HTMLElement | undefined` under this repo's `noUncheckedIndexedAccess`.
 */
export const DetailsOpen: StoryObj<typeof meta> = {
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole('button', { name: /link changed/iu }));
	},
};
