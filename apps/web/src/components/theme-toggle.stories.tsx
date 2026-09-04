import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { ThemeToggle } from './theme-toggle';

const meta = {
	args: { theme: 'light' },
	component: ThemeToggle,
	title: 'Shell/ThemeToggle',
} satisfies Meta<typeof ThemeToggle>;

export default meta;

export const Light: StoryObj<typeof meta> = { args: { theme: 'light' } };
export const Dark: StoryObj<typeof meta> = { args: { theme: 'dark' } };
