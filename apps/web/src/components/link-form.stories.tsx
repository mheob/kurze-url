import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { LinkForm, type LinkFormValues } from './link-form';

const meta = {
	component: LinkForm,
	title: 'Links/LinkForm',
} satisfies Meta<typeof LinkForm>;

export default meta;

/** The blank create-a-link state. */
export const Default: StoryObj<typeof meta> = {
	args: { onSubmit: fn<(values: LinkFormValues) => void>() },
};

/**
 * A rejected destination, lit up on the field it belongs to rather than as a
 * banner — so the a11y addon also covers the error-association wiring
 * (`aria-describedby`) here, not only on the empty-form state above.
 */
export const WithFieldError: StoryObj<typeof meta> = {
	args: {
		fieldErrors: { destination_url: 'The destination must use https://.' },
		onSubmit: fn<(values: LinkFormValues) => void>(),
	},
};

/**
 * A team with a verified domain — the only state that renders the domain
 * picker at all (Task 14), so this is what puts its `<select>`/`<label>`
 * pairing in front of the a11y addon rather than leaving it unexercised by
 * every other story here.
 */
export const WithDomainPicker: StoryObj<typeof meta> = {
	args: {
		domains: [{ hostname: 'links.verein.test', id: 'd1' }],
		onSubmit: fn<(values: LinkFormValues) => void>(),
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Default.args },
	globals: { theme: 'dark' },
};
