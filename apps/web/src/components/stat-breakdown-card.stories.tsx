import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { StatBreakdownCard } from './stat-breakdown-card';

const meta = {
	args: {
		breakdown: {
			other_clicks: 0,
			other_unique_visitors: 0,
			other_values: 0,
			values: [
				{ clicks: 512, unique_visitors: 401, value: 'Chrome' },
				{ clicks: 203, unique_visitors: 167, value: 'Safari' },
				{ clicks: 96, unique_visitors: 80, value: 'Firefox' },
			],
		},
		language: 'en',
		title: 'Browser',
	},
	component: StatBreakdownCard,
	title: 'Links/StatBreakdownCard',
} satisfies Meta<typeof StatBreakdownCard>;

export default meta;

/** A dimension with a short list — every value reported, nothing truncated. */
export const Default: StoryObj<typeof meta> = {};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette (and the
// `--chart-1` share bars drawn against it) is never checked by anything, only
// viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	globals: { theme: 'dark' },
};

/**
 * A top-ten list with a truncated remainder — the case the API's `other_*`
 * fields exist for. Without the "further values" row this card would
 * silently misstate `referrer`'s own total.
 */
export const WithOtherValues: StoryObj<typeof meta> = {
	args: {
		breakdown: {
			other_clicks: 41,
			other_unique_visitors: 33,
			other_values: 14,
			values: [
				{ clicks: 88, unique_visitors: 70, value: 'https://newsletter.sv-gruenwald.example/' },
				{ clicks: 52, unique_visitors: 44, value: 'https://facebook.com/' },
				{ clicks: 19, unique_visitors: 15, value: 'https://t.co/' },
			],
		},
		title: 'Referrer',
	},
};

/** A link whose window recorded nothing at all for this dimension. */
export const Empty: StoryObj<typeof meta> = {
	args: {
		breakdown: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
		title: 'Country',
	},
};
