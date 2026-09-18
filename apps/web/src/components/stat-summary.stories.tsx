import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { StatSummary } from './stat-summary';

const meta = {
	component: StatSummary,
	title: 'Links/StatSummary',
} satisfies Meta<typeof StatSummary>;

export default meta;

/** A link with real traffic in both binary splits — the ordinary state. */
export const Default: StoryObj<typeof meta> = {
	args: {
		botStatus: {
			other_clicks: 0,
			other_unique_visitors: 0,
			other_values: 0,
			values: [
				{ clicks: 812, unique_visitors: 640, value: 'human' },
				{ clicks: 96, unique_visitors: 41, value: 'bot' },
			],
		},
		language: 'en',
		qrVsRegular: {
			other_clicks: 0,
			other_unique_visitors: 0,
			other_values: 0,
			values: [
				{ clicks: 723, unique_visitors: 590, value: 'regular' },
				{ clicks: 185, unique_visitors: 143, value: 'qr' },
			],
		},
		totals: {
			clicks: 908,
			human_clicks: 812,
			human_unique_visitors: 640,
			unique_visitors: 681,
		},
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Default.args },
	globals: { theme: 'dark' },
};
