import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { LinkForm } from './link-form';

const meta = {
	component: LinkForm,
	title: 'Links/LinkForm',
} satisfies Meta<typeof LinkForm>;

export default meta;

/** The blank create-a-link state. */
export const Default: StoryObj<typeof meta> = {
	args: { onSubmit: fn() },
};

/**
 * A rejected destination, lit up on the field it belongs to rather than as a
 * banner — so the a11y addon also covers the error-association wiring
 * (`aria-describedby`) here, not only on the empty-form state above.
 */
export const WithFieldError: StoryObj<typeof meta> = {
	args: {
		fieldErrors: { destination_url: 'The destination must use https://.' },
		onSubmit: fn(),
	},
};
