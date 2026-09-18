import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import { StatSeriesChart } from './stat-series-chart.tsx';

/**
 * Same pattern as `link-form.test.tsx`: any component that calls
 * `useTranslation` needs an `I18nextProvider` in its tree, or `t(...)` throws
 * looking up `react-i18next`'s default context.
 *
 * @param ui - The element under test.
 * @param language - Which catalogue to load; the provider's language drives `t()`, while a component's own `language` prop drives number and date formatting.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactElement` is React's own type; not a declaration this file can edit.
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}

const SERIES = [
	{ clicks: 10, date: '2026-09-16', human_clicks: 8, human_unique_visitors: 6, unique_visitors: 7 },
	{ clicks: 0, date: '2026-09-17', human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 },
	{ clicks: 4, date: '2026-09-18', human_clicks: 4, human_unique_visitors: 3, unique_visitors: 3 },
];

describe(StatSeriesChart, () => {
	// The chart is a picture. This table is the data, and it is the only part
	// a screen reader can read at all.
	it('exposes every day as a table row', () => {
		renderWithI18n(
			<StatSeriesChart from="2026-09-16" language="en" series={SERIES} to="2026-09-18" />,
		);
		const table = screen.getByRole('table');
		// Three days plus the header row.
		expect(within(table).getAllByRole('row')).toHaveLength(4);
	});

	it('keeps a zero day as a real zero rather than dropping it', () => {
		renderWithI18n(
			<StatSeriesChart from="2026-09-16" language="en" series={SERIES} to="2026-09-18" />,
		);
		// `formatDay('2026-09-17', 'en')` under Node/jsdom's ICU, verified by
		// running it rather than assumed: `Intl.DateTimeFormat('en-US', { day:
		// 'numeric', month: 'short', year: 'numeric' })` renders "Sep 17, 2026".
		expect(screen.getByRole('table')).toHaveTextContent('Sep 17, 2026');
	});

	it('names the window and the totals in the chart image label', () => {
		renderWithI18n(
			<StatSeriesChart from="2026-09-16" language="en" series={SERIES} to="2026-09-18" />,
		);
		expect(screen.getByRole('img')).toHaveAccessibleName(/14 clicks/u);
	});

	// Final-review finding: the label named only the window and the two
	// whole-population totals, unconditionally — a reader who turns the bot
	// toggle on gets no equivalent statement of the two human totals the
	// chart is now also drawing.
	it('mentions the human totals in the chart image label once the toggle is on', async () => {
		const user = userEvent.setup();
		renderWithI18n(
			<StatSeriesChart from="2026-09-16" language="en" series={SERIES} to="2026-09-18" />,
		);
		expect(screen.getByRole('img')).not.toHaveAccessibleName(/human/iu);

		await user.click(screen.getByRole('checkbox', { name: 'Show bot share' }));

		expect(screen.getByRole('img')).toHaveAccessibleName(/12 human clicks/u);
	});

	it('starts with the bot toggle off', () => {
		renderWithI18n(
			<StatSeriesChart from="2026-09-16" language="en" series={SERIES} to="2026-09-18" />,
		);
		expect(screen.getByRole('checkbox', { name: 'Show bot share' })).not.toBeChecked();
	});

	it('adds the human columns to the table when the toggle goes on', async () => {
		const user = userEvent.setup();
		renderWithI18n(
			<StatSeriesChart from="2026-09-16" language="en" series={SERIES} to="2026-09-18" />,
		);
		expect(screen.queryByRole('columnheader', { name: 'Human clicks' })).not.toBeInTheDocument();

		await user.click(screen.getByRole('checkbox', { name: 'Show bot share' }));

		expect(screen.getByRole('columnheader', { name: 'Human clicks' })).toBeInTheDocument();
	});

	// Final-review finding: `clicks`/`human_clicks` share `--chart-1` and the
	// two visitor series share `--chart-5`, and the legend's swatch is a
	// solid square that never reads `strokeDasharray` — so the swatch alone
	// cannot tell "Clicks" apart from "Human clicks". The text has to say it
	// instead: this pins that the two dashed lines' own legend text names the
	// dash, not just the metric.
	it('names the dashed lines as dashed in the legend once the toggle is on', async () => {
		const user = userEvent.setup();
		renderWithI18n(
			<StatSeriesChart from="2026-09-16" language="en" series={SERIES} to="2026-09-18" />,
		);
		expect(screen.queryByText(/dashed/iu)).not.toBeInTheDocument();

		await user.click(screen.getByRole('checkbox', { name: 'Show bot share' }));

		expect(screen.getByText('Human clicks (dashed)')).toBeInTheDocument();
		expect(screen.getByText('Human visitors (dashed)')).toBeInTheDocument();
	});

	it('renders an empty series without throwing', () => {
		renderWithI18n(<StatSeriesChart from="2026-09-16" language="en" series={[]} to="2026-09-18" />);
		expect(screen.getByRole('table')).toBeInTheDocument();
	});
});
