import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { StatRecordedJump } from './stat-recorded-jump';

const meta = {
	args: {
		language: 'en',
		onSelect: () => undefined,
		recorded: { from: '2026-06-12', to: '2026-07-03' },
	},
	component: StatRecordedJump,
	title: 'Links/StatRecordedJump',
} satisfies Meta<typeof StatRecordedJump>;

export default meta;

export const Default: StoryObj<typeof meta> = {};

export const German: StoryObj<typeof meta> = {
	args: { language: 'de' },
};
