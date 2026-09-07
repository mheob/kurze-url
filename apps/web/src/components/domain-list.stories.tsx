import type { Domain as ApiDomain } from '@kurze-url/api-client';
import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { expect, fn, userEvent, within } from 'storybook/test';

import { DomainList } from './domain-list';

/** Mirrors `domain-list.test.tsx`'s own fixture — kept local for the same reason that file's docstring gives. */
function domain(overrides: Partial<ApiDomain> = {}): ApiDomain {
	return {
		hostname: 'links.verein.test',
		id: 'domain-1',
		records: {
			cname: { name: 'links.verein.test', value: 'cname.vercel-dns.com' },
			txt: { name: '_kurze-url-challenge.links.verein.test', value: 'example-txt-token' },
		},
		team_id: 'team-a',
		verification_status: 'pending',
		verification_token: 'example-txt-token',
		verified_at: null,
		...overrides,
	};
}

const meta = {
	// Shared across every story below: every story here renders exactly one
	// domain list with no delete in flight, unless it says otherwise.
	args: {
		deleteBlockedCount: undefined,
		deletingId: null,
		onDelete: fn(),
		onVerify: fn(),
		pendingReason: undefined,
		verifyPending: false,
		verifyingId: null,
	},
	component: DomainList,
	title: 'Domains/DomainList',
} satisfies Meta<typeof DomainList>;

export default meta;

/** A team with no custom domain yet — links keep using the shared instance hostname. */
export const Empty: StoryObj<typeof meta> = {
	args: { domains: [] },
};

/** A freshly claimed hostname: both DNS records are the whole point of this screen. */
export const Pending: StoryObj<typeof meta> = {
	args: { domains: [domain()] },
};

/**
 * The expected steady state before the maintainer has added the hostname to
 * the Vercel project — "unreachable" is not an error here, so the wording
 * must read as "not yet", not "something is wrong".
 */
export const PendingUnreachable: StoryObj<typeof meta> = {
	args: {
		domains: [domain()],
		pendingReason: 'unreachable',
		verifyingId: 'domain-1',
	},
};

/** A check in flight: the button disables so a second click cannot overlap it. */
export const VerifyPending: StoryObj<typeof meta> = {
	args: {
		domains: [domain()],
		verifyPending: true,
		verifyingId: 'domain-1',
	},
};

/** A DNS typo in the TXT record's value. */
export const PendingTokenMismatch: StoryObj<typeof meta> = {
	args: {
		domains: [domain()],
		pendingReason: 'token_mismatch',
		verifyingId: 'domain-1',
	},
};

/** Verified: the records are instructions, not decoration, so they disappear once the domain works. */
export const Verified: StoryObj<typeof meta> = {
	args: {
		domains: [domain({ verification_status: 'verified', verified_at: '2026-01-01T00:00:00Z' })],
	},
};

/** Another team verified this hostname first — a dead end for this claim, not something DNS can fix. */
export const Failed: StoryObj<typeof meta> = {
	args: {
		domains: [domain({ verification_status: 'failed' })],
	},
};

/** A mixed list: one working, one still pending. */
export const Mixed: StoryObj<typeof meta> = {
	args: {
		domains: [
			domain({
				hostname: 'links.verein.test',
				id: 'domain-1',
				verification_status: 'verified',
				verified_at: '2026-01-01T00:00:00Z',
			}),
			domain({
				hostname: 'kurz.other-verein.test',
				id: 'domain-2',
				records: {
					cname: { name: 'kurz.other-verein.test', value: 'cname.vercel-dns.com' },
					txt: {
						name: '_kurze-url-challenge.kurz.other-verein.test',
						value: 'example-txt-token-two',
					},
				},
				verification_token: 'example-txt-token-two',
			}),
		],
	},
};

/**
 * The armed, labelled-alertdialog state of a row's delete control, reached
 * the same way `ConfirmDelete`'s own `Armed` story reaches it — a `play`
 * function, since `armed` is internal `useState` with no args-only way in.
 * This is what proves the dialog still passes axe once nested inside a list
 * row, not only in `ConfirmDelete`'s own isolated story.
 */
export const DeleteArmed: StoryObj<typeof meta> = {
	args: { domains: [domain()] },
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole('button', { name: 'Delete links.verein.test' }));
		await expect(canvas.getByRole('alertdialog')).toBeInTheDocument();
	},
};

/**
 * The API refuses to delete a domain that still has links (409) — the
 * blocking count is the whole point of the refusal, so it renders next to
 * the row it is about rather than collapsing into a generic error.
 */
export const DeleteBlockedByLinks: StoryObj<typeof meta> = {
	args: { deleteBlockedCount: 3, deletingId: 'domain-1', domains: [domain()] },
};
