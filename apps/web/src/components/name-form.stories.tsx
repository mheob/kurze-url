import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { NameForm } from './name-form';

const meta = {
	args: {
		label: 'Folder name',
		onSubmit: fn<(name: string) => void>(),
		submitLabel: 'Create folder',
	},
	component: NameForm,
	title: 'Names/NameForm',
} satisfies Meta<typeof NameForm>;

export default meta;

/** The create form on its own: one labelled field and its submit button, nothing typed yet. */
export const Default: StoryObj<typeof meta> = {};

/**
 * A server-side failure — a taken name, the 100-folder cap, or an invalid
 * name — all render the same way: the translated message on the field
 * itself, via `error`. Nothing else in this file's own test suite renders
 * the create form's error state, so without this story it never reached the
 * Storybook a11y check at all (folders-frontend final review, Minor 8).
 */
export const ServerError: StoryObj<typeof meta> = {
	args: { error: 'A folder with this name already exists.' },
};
