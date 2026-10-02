import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { createRef } from 'react';
import { fn } from 'storybook/test';

import { NameManagementBody } from './name-management-body';

const meta = {
	// Shared across every story below: an editor looking at a team's folders
	// with no failure showing, unless a story says otherwise.
	args: {
		canEdit: true,
		createError: undefined,
		createKey: 0,
		headingRef: createRef<HTMLHeadingElement>(),
		items: [
			{ id: 'f1', name: 'Newsletter' },
			{ id: 'f2', name: 'Sommerfest' },
		],
		namespace: 'folders',
		onCreate: fn<(name: string) => void>(),
		onDelete: fn<(itemId: string) => void>(),
		onDismissError: fn<(itemId: string) => void>(),
		// oxlint-disable-next-line typescript/require-await -- stands in for a rename call the real hook awaits; the fixture has nothing to await itself.
		onRename: fn<(itemId: string, name: string) => Promise<boolean>>(async () => true),
		rowError: null,
		teamSlug: 'verein',
	},
	component: NameManagementBody,
	title: 'Names/NameManagementBody',
} satisfies Meta<typeof NameManagementBody>;

export default meta;

/** An editor on the folders page: the create form above the list. */
export const Folders: StoryObj<typeof meta> = {};

/** The same body for tags: the tags copy throughout. */
export const Tags: StoryObj<typeof meta> = {
	args: {
		items: [
			{ id: 't1', name: 'Presse' },
			{ id: 't2', name: 'Vorstand' },
		],
		namespace: 'tags',
	},
};

/** A viewer gets the list alone: no create form, and no rename or delete control. */
export const Viewer: StoryObj<typeof meta> = {
	args: { canEdit: false },
};

/** A server-side failure on create lands on the name field itself. */
export const CreateError: StoryObj<typeof meta> = {
	args: { createError: 'A folder with this name already exists.' },
};
