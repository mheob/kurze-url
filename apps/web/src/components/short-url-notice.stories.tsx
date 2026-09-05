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
