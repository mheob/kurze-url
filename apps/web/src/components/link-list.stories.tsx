import type { Link as ApiLink, PageLink } from '@kurze-url/api-client';
import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { LinkList } from './link-list';

/** Mirrors `link-list.test.tsx`'s own fixture — kept local rather than shared, the same reasoning that file's own docstring gives for building fixtures inline. */
function link(overrides: Partial<ApiLink> = {}): ApiLink {
	return {
		analytics_enabled: true,
		created_at: '2026-01-01T00:00:00Z',
		created_by: 'user-1',
		destination_url: 'https://example.org/',
		domain_id: 'domain-1',
		expires_at: null,
		folder_id: 'folder-1',
		has_password: false,
		hostname: 'kurze.url',
		id: 'link-1',
		redirect_type: 302,
		short_url: 'https://kurze.url/abc123',
		slug: 'abc123',
		state: 'active',
		tags: [],
		team_id: 'team-a',
		updated_at: '2026-01-01T00:00:00Z',
		...overrides,
	};
}

function pageOf(overrides: Partial<PageLink> = {}): PageLink {
	return { items: [], page: 1, per_page: 20, total_count: 0, ...overrides };
}

const meta = {
	component: LinkList,
	title: 'Links/LinkList',
} satisfies Meta<typeof LinkList>;

export default meta;

/**
 * A team with no links yet — the empty state is a prompt with an actual link
 * into link creation (Finding 2), not a dead end.
 */
export const Empty: StoryObj<typeof meta> = {
	args: { data: pageOf(), page: 1, teamId: 'team-a' },
};

/** A team that already has links: each row offers copy and edit. */
export const Populated: StoryObj<typeof meta> = {
	args: {
		data: pageOf({
			items: [
				link(),
				link({
					destination_url: 'https://example.org/other',
					id: 'link-2',
					short_url: 'https://kurze.url/def456',
					slug: 'def456',
				}),
			],
			total_count: 2,
		}),
		page: 1,
		teamId: 'team-a',
	},
};

/**
 * The shared instance's placeholder hostname before a real short domain is
 * configured — `ShortUrlNotice`'s `.invalid` state, surfaced here in the
 * context it actually renders in rather than only in isolation.
 */
export const NoShortDomainConfigured: StoryObj<typeof meta> = {
	args: {
		data: pageOf({
			items: [link({ hostname: 'short.invalid', short_url: 'https://short.invalid/abc123' })],
			total_count: 1,
		}),
		page: 1,
		teamId: 'team-a',
	},
};
