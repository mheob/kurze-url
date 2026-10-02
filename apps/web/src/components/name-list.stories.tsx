import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { NameList, type NamedItem } from './name-list';

/**
 * Mirrors `name-list.test.tsx`'s own fixture — kept local for the same reason `domain-list.stories.tsx`'s docstring gives.
 *
 * @param overrides - Partial fields to override on the default item fixture.
 * @returns The item fixture.
 */
function item(overrides: Readonly<Partial<NamedItem>> = {}): NamedItem {
	return { id: 'f1', name: 'Newsletter', ...overrides };
}

const meta = {
	// Shared across every story below: every story here renders exactly one
	// folder list with no row failure, unless it says otherwise.
	args: {
		canEdit: true,
		namespace: 'folders',
		onDelete: fn<(itemId: string) => void>(),
		onDismissError: fn<(itemId: string) => void>(),
		// oxlint-disable-next-line typescript/require-await -- stands in for a rename call the real route awaits; the fixture has nothing to await itself.
		onRename: fn<(itemId: string, name: string) => Promise<boolean>>(async () => true),
		rowError: null,
		teamSlug: 'verein',
	},
	component: NameList,
	title: 'Names/NameList',
} satisfies Meta<typeof NameList>;

export default meta;

/** A team with no folders yet — an editor gets a hint to create the first one. */
export const Empty: StoryObj<typeof meta> = {
	args: { canEdit: true, items: [] },
};

/** An editor sees every folder plus rename and delete controls on each row. */
export const Editor: StoryObj<typeof meta> = {
	args: {
		canEdit: true,
		items: [item(), item({ id: 'f2', name: 'Sommerfest' })],
	},
};

/** A viewer sees the same folders, but no rename or delete control. */
export const Viewer: StoryObj<typeof meta> = {
	args: {
		canEdit: false,
		items: [item(), item({ id: 'f2', name: 'Sommerfest' })],
	},
};

/**
 * A delete that failed shows the row's own alert, with the row left in
 * place — the closed-row branch this story actually renders, since nothing
 * here opens the rename form. A rename failure instead renders inside the
 * open rename form itself (`NameRowError`'s own docstring), which a static
 * story can't show without user interaction to open that form first.
 */
export const RowError: StoryObj<typeof meta> = {
	args: {
		canEdit: true,
		items: [item(), item({ id: 'f2', name: 'Sommerfest' })],
		rowError: {
			action: 'delete',
			itemId: 'f2',
			message: 'A folder with this name already exists.',
		},
	},
};

/** The same list for tags: the tags copy, and each name links to the tag filter instead of the folder one. */
export const Tags: StoryObj<typeof meta> = {
	args: {
		canEdit: true,
		items: [item({ id: 't1', name: 'Presse' }), item({ id: 't2', name: 'Vorstand' })],
		namespace: 'tags',
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Editor.args },
	globals: { theme: 'dark' },
};
