import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { CopyButton } from './copy-button';

const meta = {
	component: CopyButton,
	title: 'Links/CopyButton',
} satisfies Meta<typeof CopyButton>;

export default meta;

export const Default: StoryObj<typeof meta> = {
	args: { value: 'https://kurze.url/abc123' },
};
