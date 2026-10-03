import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import type { AuditFilters } from '../lib/audit-filters';
import { AuditFilterBar } from './audit-filter-bar';

const meta = {
	args: {
		filters: { page: 1 },
		language: 'en',
		members: [
			{ email: 'anna@example.org', user_id: 'user-a' },
			{ email: 'bernd@example.org', user_id: 'user-b' },
		],
		onChange: fn<(filters: AuditFilters) => void>(),
		retainedSince: '2024-10-03',
	},
	component: AuditFilterBar,
	// Shares `AuditEntryTable`'s namespace: both belong to the audit log page,
	// not to `Links/`.
	title: 'Audit/AuditFilterBar',
} satisfies Meta<typeof AuditFilterBar>;

export default meta;

/**
 * No filter set yet: every control at its "any"/empty state, no reset button,
 * and the retention note under the bar with both date fields bounded by it.
 */
export const Default: StoryObj<typeof meta> = {};

/**
 * Before the API has named the retention floor — the bar as a caller without
 * `retained_since` renders it: no note, and no `min` on either date field.
 */
export const WithoutRetentionFloor: StoryObj<typeof meta> = {
	args: { retainedSince: undefined },
};

/** A filter already chosen on every axis, so the reset button renders too. */
export const Filtered: StoryObj<typeof meta> = {
	args: {
		filters: { actor: 'user-b', entityType: 'link', from: '2026-03-01', page: 1, to: '2026-03-31' },
	},
};
