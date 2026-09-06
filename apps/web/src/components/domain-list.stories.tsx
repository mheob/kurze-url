import type { Domain as ApiDomain } from '@kurze-url/api-client';
import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { DomainList } from './domain-list';

/** Mirrors `domain-list.test.tsx`'s own fixture — kept local for the same reason that file's docstring gives. */
function domain(overrides: Partial<ApiDomain> = {}): ApiDomain {
	return {
		hostname: 'links.verein.test',
		id: 'domain-1',
		records: {
			cname: { name: 'links.verein.test', value: 'cname.vercel-dns.com' },
			txt: { name: '_kurze-url-challenge.links.verein.test', value: 'a1b2c3d4e5f6' },
		},
		team_id: 'team-a',
		verification_status: 'pending',
		verification_token: 'a1b2c3d4e5f6',
		verified_at: null,
		...overrides,
	};
}

const meta = {
	component: DomainList,
	title: 'Domains/DomainList',
} satisfies Meta<typeof DomainList>;

export default meta;

/** A team with no custom domain yet — links keep using the shared instance hostname. */
export const Empty: StoryObj<typeof meta> = {
	args: { domains: [], onVerify: fn(), pendingReason: undefined, verifyingId: null },
};

/** A freshly claimed hostname: both DNS records are the whole point of this screen. */
export const Pending: StoryObj<typeof meta> = {
	args: { domains: [domain()], onVerify: fn(), pendingReason: undefined, verifyingId: null },
};

/**
 * The expected steady state before the maintainer has added the hostname to
 * the Vercel project — "unreachable" is not an error here, so the wording
 * must read as "not yet", not "something is wrong".
 */
export const PendingUnreachable: StoryObj<typeof meta> = {
	args: {
		domains: [domain()],
		onVerify: fn(),
		pendingReason: 'unreachable',
		verifyingId: 'domain-1',
	},
};

/** A DNS typo in the TXT record's value. */
export const PendingTokenMismatch: StoryObj<typeof meta> = {
	args: {
		domains: [domain()],
		onVerify: fn(),
		pendingReason: 'token_mismatch',
		verifyingId: 'domain-1',
	},
};

/** Verified: the records are instructions, not decoration, so they disappear once the domain works. */
export const Verified: StoryObj<typeof meta> = {
	args: {
		domains: [domain({ verification_status: 'verified', verified_at: '2026-01-01T00:00:00Z' })],
		onVerify: fn(),
		pendingReason: undefined,
		verifyingId: null,
	},
};

/** Another team verified this hostname first — a dead end for this claim, not something DNS can fix. */
export const Failed: StoryObj<typeof meta> = {
	args: {
		domains: [domain({ verification_status: 'failed' })],
		onVerify: fn(),
		pendingReason: undefined,
		verifyingId: null,
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
					txt: { name: '_kurze-url-challenge.kurz.other-verein.test', value: 'f6e5d4c3b2a1' },
				},
				verification_token: 'f6e5d4c3b2a1',
			}),
		],
		onVerify: fn(),
		pendingReason: undefined,
		verifyingId: null,
	},
};
