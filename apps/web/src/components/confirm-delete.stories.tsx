import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { expect, fn, userEvent, within } from 'storybook/test';

import { ConfirmDelete } from './confirm-delete';

const meta = {
	args: { label: 'Delete', onConfirm: fn() },
	component: ConfirmDelete,
	title: 'Links/ConfirmDelete',
} satisfies Meta<typeof ConfirmDelete>;

export default meta;

/** The single button shown before anything is armed. */
export const Default: StoryObj<typeof meta> = {};

/**
 * The armed, labelled-alertdialog state. `armed` is internal `useState`, not
 * a prop, so there is no args-only way to reach it — a `play` function that
 * actually clicks through is what lets the a11y addon audit this state too,
 * not only the unarmed default above.
 */
export const Armed: StoryObj<typeof meta> = {
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole('button', { name: 'Delete' }));
		await expect(canvas.getByRole('alertdialog')).toBeInTheDocument();
	},
};
