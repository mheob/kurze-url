/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file traces to `@kurze-url/api-client`'s generated `Link`/`PageLink`/`Folder` types (`Link.tags`'s
   nested array included), whose properties are not marked readonly; that is generated codegen
   output, never edited by hand. */

import type { Folder, Link as ApiLink, PageLink } from '@kurze-url/api-client';
import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { LinkList } from './link-list';

/**
 * Mirrors `link-list.test.tsx`'s own fixture — kept local rather than shared, the same reasoning that file's own docstring gives for building fixtures inline.
 *
 * @param overrides - Partial fields to override on the default link fixture.
 * @returns The link fixture.
 */
function link(overrides: Partial<ApiLink> = {}): ApiLink {
	return {
		analytics_enabled: true,
		created_at: '2026-01-01T00:00:00Z',
		created_by: 'user-1',
		destination_url: 'https://example.org/',
		domain_id: 'domain-1',
		expires_at: null,
		has_password: false,
		hostname: 'kurze.url',
		id: 'link-1',
		redirect_type: 302,
		short_url: 'https://kurze.url/abc123',
		slug: 'abc123',
		state: 'active',
		tags: [],
		team_id: 'a',
		updated_at: '2026-01-01T00:00:00Z',
		...overrides,
	};
}

function pageOf(overrides: Partial<PageLink> = {}): PageLink {
	return { items: [], page: 1, per_page: 20, total_count: 0, ...overrides };
}

const FOLDERS: readonly Folder[] = [
	{ created_at: '2026-09-26T00:00:00Z', id: 'folder-1', name: 'Sommerfest', team_id: 'a' },
	{ created_at: '2026-09-26T00:00:00Z', id: 'folder-2', name: 'Vorstand', team_id: 'a' },
];

const meta = {
	args: {
		folder: undefined,
		folders: FOLDERS,
		onFolderChange: fn<(folder: string | undefined) => void>(),
	},
	component: LinkList,
	title: 'Links/LinkList',
} satisfies Meta<typeof LinkList>;

export default meta;

/**
 * A team with no links yet — the empty state is a prompt with an actual link
 * into link creation (Finding 2), not a dead end.
 */
export const Empty: StoryObj<typeof meta> = {
	args: { data: pageOf(), page: 1, teamSlug: 'verein-a' },
};

/** A team that already has links: each row offers copy and edit. */
export const Populated: StoryObj<typeof meta> = {
	args: {
		data: pageOf({
			items: [
				link({ folder_id: 'folder-1' }),
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
		teamSlug: 'verein-a',
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
		teamSlug: 'verein-a',
	},
};

/**
 * The folder column, populated: one link filed under "Sommerfest", one
 * unfiled — the "–" cell's visually hidden "No folder" text is what a
 * screen-reader user hears there instead of a bare dash.
 */
export const WithFolderColumn: StoryObj<typeof meta> = {
	args: {
		data: pageOf({
			items: [
				link({ folder_id: 'folder-1', id: 'link-1' }),
				link({ id: 'link-2', short_url: 'https://kurze.url/def456', slug: 'def456' }),
			],
			total_count: 2,
		}),
		page: 1,
		teamSlug: 'verein-a',
	},
};

/**
 * The list filtered to one folder: the "Folder: Sommerfest" context line, the
 * filter select reflecting the active choice, and the "New link" link
 * carrying the folder along to preselect it on the create form.
 */
export const FilteredToFolder: StoryObj<typeof meta> = {
	args: {
		data: pageOf({ items: [link({ folder_id: 'folder-1', id: 'link-1' })], total_count: 1 }),
		folder: 'folder-1',
		page: 1,
		teamSlug: 'verein-a',
	},
};

/**
 * "No folder" filtered to zero links: distinct wording ("Every link is in a
 * folder.") from both the plain empty state and the in-folder one, since all
 * three describe a different reason the table is missing.
 */
export const UnfiledEmpty: StoryObj<typeof meta> = {
	args: { data: pageOf(), folder: 'none', page: 1, teamSlug: 'verein-a' },
};

/**
 * The `folder` search value names no folder the team has (deleted, or
 * hand-edited into the URL) — the list refuses to silently discard the
 * filter, and offers a way back to the unfiltered list instead of guessing.
 */
export const MissingFolder: StoryObj<typeof meta> = {
	args: {
		data: pageOf(),
		folder: '0b7c1f6e-2f4a-4f7e-9a53-8a0e1d2c3b4a',
		page: 1,
		teamSlug: 'verein-a',
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Populated.args },
	globals: { theme: 'dark' },
};
