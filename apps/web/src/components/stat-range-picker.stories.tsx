import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { expect, fn, screen, userEvent, within } from 'storybook/test';

import type { StatsWindow } from '../lib/stats-window';
import { StatRangePicker } from './stat-range-picker';

const TODAY = new Date('2026-09-18T11:30:00Z');

const meta = {
	args: {
		language: 'en',
		onChange: fn<(window: Readonly<StatsWindow>) => void>(),
		today: TODAY,
		window: { from: '2026-08-20', to: '2026-09-18' },
	},
	component: StatRangePicker,
	title: 'Links/StatRangePicker',
} satisfies Meta<typeof StatRangePicker>;

export default meta;

/** The ordinary state: a thirty-day window, so the "30 days" preset reads as pressed. */
export const Default: StoryObj<typeof meta> = {};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	globals: { theme: 'dark' },
};

/**
 * The open-popover state. `Popover` owns whether it is open, not a prop, so
 * there is no args-only way to reach it — a `play` function that actually
 * clicks the trigger is what lets the a11y addon audit the calendar, the
 * disabled-date semantics react-day-picker emits, and the popover's own
 * accessible name, not only the closed trigger button the default state
 * would otherwise leave it checking. The assertion queries `screen`, not
 * `within(canvasElement)`: `PopoverContent` renders through a portal, so once
 * open it is not a descendant of this story's own canvas element — the same
 * reason `ConfirmDelete`'s `Armed` story does the same thing for its dialog.
 */
export const CalendarOpen: StoryObj<typeof meta> = {
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole('button', { name: 'Choose dates' }));
		await expect(screen.getByRole('dialog', { name: 'Choose dates' })).toBeInTheDocument();
	},
};
