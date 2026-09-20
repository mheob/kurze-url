import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { rolesAssignableBy, type TeamRole } from '../lib/team-roles';
import { MemberInviteForm } from './member-invite-form';

interface InviteValues {
	readonly email: string;
	readonly role: TeamRole;
}

const meta = {
	component: MemberInviteForm,
	title: 'Members/MemberInviteForm',
} satisfies Meta<typeof MemberInviteForm>;

export default meta;

/**
 * An admin's own ceiling: every role below `owner`, which
 * `rolesAssignableBy` refuses to offer to anyone who isn't one themselves.
 */
export const AdminRoles: StoryObj<typeof meta> = {
	args: {
		failure: null,
		onSubmit: fn<(values: InviteValues) => void>(),
		pending: false,
		result: null,
		roles: rolesAssignableBy('admin'),
	},
};

/** An owner, the one rank that may also grant `owner` — the fourth option this select gets that the admin story above does not. */
export const OwnerRoles: StoryObj<typeof meta> = {
	args: {
		failure: null,
		onSubmit: fn<(values: InviteValues) => void>(),
		pending: false,
		result: null,
		roles: rolesAssignableBy('owner'),
	},
};

/** Mid-submission: the button disables, nothing else about the form changes. */
export const Pending: StoryObj<typeof meta> = {
	args: {
		failure: null,
		onSubmit: fn<(values: InviteValues) => void>(),
		pending: true,
		result: null,
		roles: rolesAssignableBy('admin'),
	},
};

/**
 * The address already had an account on this instance: added on the spot,
 * nobody notified — the case Task 1 added `invited` to the API to make
 * distinguishable from an actual invitation email going out.
 */
export const SilentAdd: StoryObj<typeof meta> = {
	args: {
		failure: null,
		onSubmit: fn<(values: InviteValues) => void>(),
		pending: false,
		result: { email: 'schon-dabei@verein.test', invited: false },
		roles: rolesAssignableBy('admin'),
	},
};

/** The instance-wide monthly invitation budget is spent; only the maintainer can do anything about it. */
export const InstanceBudgetFailure: StoryObj<typeof meta> = {
	args: {
		failure: 'instanceBudget',
		onSubmit: fn<(values: InviteValues) => void>(),
		pending: false,
		result: null,
		roles: rolesAssignableBy('admin'),
	},
};
