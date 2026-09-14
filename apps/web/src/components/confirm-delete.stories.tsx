import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { expect, fn, screen, userEvent, within } from 'storybook/test';

import { ConfirmDelete } from './confirm-delete';

const meta = {
	args: {
		label: 'Delete',
		onConfirm: fn<() => void>(),
		question: 'Delete this link? Anyone who already has the short URL will get a 404.',
	},
	component: ConfirmDelete,
	title: 'Links/ConfirmDelete',
} satisfies Meta<typeof ConfirmDelete>;

export default meta;

/** The single trigger button shown before the dialog is opened. */
export const Default: StoryObj<typeof meta> = {};

/**
 * The open, labelled-alertdialog state. `AlertDialog` owns whether it is
 * open, not a prop, so there is no args-only way to reach it — a `play`
 * function that actually clicks through is what lets the a11y addon audit
 * this state too, not only the closed default above. The assertion queries
 * `screen`, not `within(canvasElement)`: `AlertDialogContent` renders
 * through a portal, so once open the dialog is not a descendant of this
 * story's own canvas element.
 */
export const Armed: StoryObj<typeof meta> = {
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole('button', { name: 'Delete' }));
		await expect(screen.getByRole('alertdialog')).toBeInTheDocument();
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs
// every story at its defaults — so without this story the dialog's dark
// palette is never checked by anything, only viewable by hand. Armed, not
// closed: the dialog is the most visually-changed part of this component, and
// `Armed` above already proves the `play` function that opens it, so `Dark`
// reuses the same play function rather than only re-checking the closed
// trigger button.
export const Dark: StoryObj<typeof meta> = {
	globals: { theme: 'dark' },
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole('button', { name: 'Delete' }));
		await expect(screen.getByRole('alertdialog')).toBeInTheDocument();
	},
};
