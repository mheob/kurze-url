import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { expect, userEvent, within } from 'storybook/test';

import { StatSeriesChart } from './stat-series-chart';

const SERIES = [
	{
		clicks: 42,
		date: '2026-09-10',
		human_clicks: 38,
		human_unique_visitors: 30,
		unique_visitors: 34,
	},
	{
		clicks: 51,
		date: '2026-09-11',
		human_clicks: 44,
		human_unique_visitors: 33,
		unique_visitors: 39,
	},
	// A day with no traffic at all — the gap-filled zero the chart and the
	// table both have to render as a real point/row, not skip.
	{ clicks: 0, date: '2026-09-12', human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 },
	{
		clicks: 60,
		date: '2026-09-13',
		human_clicks: 52,
		human_unique_visitors: 41,
		unique_visitors: 46,
	},
	{
		clicks: 47,
		date: '2026-09-14',
		human_clicks: 39,
		human_unique_visitors: 29,
		unique_visitors: 35,
	},
	{
		clicks: 55,
		date: '2026-09-15',
		human_clicks: 46,
		human_unique_visitors: 36,
		unique_visitors: 41,
	},
	{
		clicks: 63,
		date: '2026-09-16',
		human_clicks: 54,
		human_unique_visitors: 43,
		unique_visitors: 48,
	},
];

const meta = {
	args: {
		from: '2026-09-10',
		language: 'en',
		series: SERIES,
		to: '2026-09-16',
	},
	component: StatSeriesChart,
	title: 'Links/StatSeriesChart',
} satisfies Meta<typeof StatSeriesChart>;

export default meta;

/**
 * The ordinary state: the bot toggle off, two lines, and the chart's own
 * image label. Also where the browser-vs-Node ICU check for `formatDay`
 * lives: the hidden table's date cells go through the exact same formatter
 * the chart's axis ticks do, so asserting one string here is a real render
 * in Chromium, not a re-run of `stat-series-chart.test.tsx`'s Node/jsdom
 * assertion of the same value. A month-abbreviation mismatch between the two
 * runtimes is the realistic failure this catches — before a Verein reports
 * a hydration flicker instead.
 */
export const Default: StoryObj<typeof meta> = {
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		// `formatDay('2026-09-16', 'en')` measured under Node/jsdom: "Sep 16, 2026".
		await expect(canvas.getByRole('table')).toHaveTextContent('Sep 16, 2026');
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the chart's dark palette (and
// the `--chart-1`/`--chart-5` lines drawn against it) is never checked by
// anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	globals: { theme: 'dark' },
};

/**
 * The toggle on: four lines, four table columns. The toggle owns its own
 * `useState`, so there is no args-only way to reach this state — a `play`
 * function that actually clicks it is what lets the a11y addon audit this
 * state too, not only the two-line default above.
 */
export const BotsShown: StoryObj<typeof meta> = {
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole('checkbox', { name: 'Show bot share' }));
		await expect(canvas.getByRole('columnheader', { name: 'Human clicks' })).toBeInTheDocument();
	},
};
