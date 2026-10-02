import type { Folder, Tag } from '@kurze-url/api-client';
import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { LinkFilterBar } from './link-filter-bar';

const FOLDERS: readonly Folder[] = [
	{ created_at: '2026-09-26T00:00:00Z', id: 'folder-1', name: 'Sommerfest', team_id: 'a' },
	{ created_at: '2026-09-26T00:00:00Z', id: 'folder-2', name: 'Vorstand', team_id: 'a' },
];

const TAGS: readonly Tag[] = [
	{ id: 'tag-1', name: 'Jugend', team_id: 'a' },
	{ id: 'tag-2', name: 'Presse', team_id: 'a' },
];

const meta = {
	args: {
		folder: undefined,
		folders: FOLDERS,
		onChange: fn<(next: Readonly<{ folder?: string; tag?: string }>) => void>(),
		tag: undefined,
		tags: TAGS,
	},
	component: LinkFilterBar,
	title: 'Links/LinkFilterBar',
} satisfies Meta<typeof LinkFilterBar>;

export default meta;

/** No filter set yet: both selects on their "all" option. */
export const Default: StoryObj<typeof meta> = {};

/** A folder and a tag chosen together; changing either one reports the other unchanged. */
export const BothFiltered: StoryObj<typeof meta> = {
	args: { folder: 'folder-1', tag: 'tag-2' },
};
