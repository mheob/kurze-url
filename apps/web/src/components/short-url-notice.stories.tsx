import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { ShortUrlNotice } from './short-url-notice';

const meta = {
	component: ShortUrlNotice,
	title: 'Links/ShortUrlNotice',
} satisfies Meta<typeof ShortUrlNotice>;

export default meta;

export const NoDomainConfigured: StoryObj<typeof meta> = {
	args: { hostname: 'short.invalid' },
};

export const RealDomainConfigured: StoryObj<typeof meta> = {
	args: { hostname: 'kurze.url' },
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs
// every story at its defaults — so without this story the destructive tokens
// this notice draws on (`border-destructive/50`, `bg-destructive/10`) are
// never checked in dark mode by anything, only viewable by hand.
// `NoDomainConfigured`, not `RealDomainConfigured`: the latter renders
// nothing at all (the component returns null for a real hostname), so it has
// no dark-mode rendering to check.
export const Dark: StoryObj<typeof meta> = {
	args: { ...NoDomainConfigured.args },
	globals: { theme: 'dark' },
};
