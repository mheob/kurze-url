import type { Folder as ApiFolder } from '@kurze-url/api-client';
import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { FolderList } from './folder-list';

/**
 * Mirrors `folder-list.test.tsx`'s own fixture — kept local for the same reason `domain-list.stories.tsx`'s docstring gives.
 *
 * @param overrides - Partial fields to override on the default folder fixture.
 * @returns The folder fixture.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ApiFolder` is `@kurze-url/api-client`'s generated `Folder` type, whose properties are not marked readonly; that is generated codegen output, never edited by hand.
function folder(overrides: Partial<ApiFolder> = {}): ApiFolder {
	return {
		created_at: '2026-09-26T00:00:00Z',
		id: 'f1',
		name: 'Newsletter',
		team_id: 'team-a',
		...overrides,
	};
}

const meta = {
	// Shared across every story below: every story here renders exactly one
	// folder list with no row failure, unless it says otherwise.
	args: {
		canEdit: true,
		onDelete: fn<(folderId: string) => void>(),
		onDismissError: fn<(folderId: string) => void>(),
		// oxlint-disable-next-line typescript/require-await -- stands in for a rename call the real route awaits; the fixture has nothing to await itself.
		onRename: fn<(folderId: string, name: string) => Promise<boolean>>(async () => true),
		rowError: null,
		teamSlug: 'verein',
	},
	component: FolderList,
	title: 'Folders/FolderList',
} satisfies Meta<typeof FolderList>;

export default meta;

/** A team with no folders yet — an editor gets a hint to create the first one. */
export const Empty: StoryObj<typeof meta> = {
	args: { canEdit: true, folders: [] },
};

/** An editor sees every folder plus rename and delete controls on each row. */
export const Editor: StoryObj<typeof meta> = {
	args: {
		canEdit: true,
		folders: [folder(), folder({ id: 'f2', name: 'Sommerfest' })],
	},
};

/** A viewer sees the same folders, but no rename or delete control. */
export const Viewer: StoryObj<typeof meta> = {
	args: {
		canEdit: false,
		folders: [folder(), folder({ id: 'f2', name: 'Sommerfest' })],
	},
};

/**
 * A delete that failed shows the row's own alert, with the row left in
 * place — the closed-row branch this story actually renders, since nothing
 * here opens the rename form. A rename failure instead renders inside the
 * open rename form itself (`FolderRowError`'s own docstring), which a static
 * story can't show without user interaction to open that form first.
 */
export const RowError: StoryObj<typeof meta> = {
	args: {
		canEdit: true,
		folders: [folder(), folder({ id: 'f2', name: 'Sommerfest' })],
		rowError: {
			action: 'delete',
			folderId: 'f2',
			message: 'A folder with this name already exists.',
		},
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Editor.args },
	globals: { theme: 'dark' },
};
