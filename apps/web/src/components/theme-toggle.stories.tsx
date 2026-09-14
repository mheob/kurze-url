import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { ThemeToggle } from './theme-toggle';

const meta = {
	args: { theme: 'light' },
	component: ThemeToggle,
	title: 'Shell/ThemeToggle',
} satisfies Meta<typeof ThemeToggle>;

export default meta;

export const Light: StoryObj<typeof meta> = { args: { theme: 'light' } };
// `args.theme` is what `ThemeToggle` itself renders from; `globals.theme` is
// what `preview.tsx`'s decorator reads to add the `dark` class the rest of
// the page's tokens key off. Without the latter this story rendered the
// light palette regardless of its own name — see `app-sidebar.stories.tsx`'s
// `Dark` story for the same pairing.
export const Dark: StoryObj<typeof meta> = { args: { theme: 'dark' }, globals: { theme: 'dark' } };
