import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import { StatSummary } from './stat-summary';

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

const TOTALS = {
	clicks: 1234,
	human_clicks: 900,
	human_unique_visitors: 700,
	unique_visitors: 1000,
};
const EMPTY_BREAKDOWN = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: null,
};

describe(StatSummary, () => {
	it('formats every total for the active language', () => {
		renderWithI18n(
			<StatSummary
				botStatus={EMPTY_BREAKDOWN}
				language="de"
				qrVsRegular={EMPTY_BREAKDOWN}
				totals={TOTALS}
			/>,
		);
		expect(screen.getByText('1.234')).toBeInTheDocument();
		expect(screen.getByText('1.000')).toBeInTheDocument();
	});

	// The field name says the opposite of what the number means, so the caveat
	// is visible text rather than a tooltip.
	it('states the per-day caveat next to the visitor figure', () => {
		renderWithI18n(
			<StatSummary
				botStatus={EMPTY_BREAKDOWN}
				language="en"
				qrVsRegular={EMPTY_BREAKDOWN}
				totals={TOTALS}
			/>,
		);
		expect(screen.getByText(/counts three times/iu)).toBeInTheDocument();
	});

	it('shows each binary split with its share', () => {
		renderWithI18n(
			<StatSummary
				botStatus={{
					other_clicks: 0,
					other_unique_visitors: 0,
					other_values: 0,
					values: [
						{ clicks: 750, unique_visitors: 700, value: 'human' },
						{ clicks: 250, unique_visitors: 200, value: 'bot' },
					],
				}}
				language="en"
				qrVsRegular={EMPTY_BREAKDOWN}
				totals={TOTALS}
			/>,
		);
		// The raw wire value is 'human' — `splitValueLabel` translates the
		// closed four-token set (`human`/`bot`/`regular`/`qr`) rather than
		// rendering it verbatim, unlike `StatBreakdownCard`'s open dimensions.
		expect(screen.getByText('Human')).toBeInTheDocument();
		expect(screen.getByText('75%')).toBeInTheDocument();
	});

	// Final-review finding: an unrecognised value — the API's dimension set
	// can grow — must still render, verbatim, rather than a blank string or
	// i18next's own missing-key marker.
	it('renders an unrecognised split value verbatim', () => {
		renderWithI18n(
			<StatSummary
				botStatus={{
					other_clicks: 0,
					other_unique_visitors: 0,
					other_values: 0,
					values: [{ clicks: 10, unique_visitors: 10, value: 'crawler' }],
				}}
				language="en"
				qrVsRegular={EMPTY_BREAKDOWN}
				totals={TOTALS}
			/>,
		);
		expect(screen.getByText('crawler')).toBeInTheDocument();
	});

	// A link with no QR clicks has one value, not two. Assuming a pair is the
	// easiest way to crash this component on real data.
	it('renders a split that holds only one value', () => {
		renderWithI18n(
			<StatSummary
				botStatus={EMPTY_BREAKDOWN}
				language="en"
				qrVsRegular={{
					other_clicks: 0,
					other_unique_visitors: 0,
					other_values: 0,
					values: [{ clicks: 12, unique_visitors: 10, value: 'regular' }],
				}}
				totals={TOTALS}
			/>,
		);
		// The raw wire value is 'regular' — see the comment on the 'human'
		// assertion above.
		expect(screen.getByText('Direct')).toBeInTheDocument();
		expect(screen.getByText('100%')).toBeInTheDocument();
	});

	it('says so when a split has no values at all', () => {
		renderWithI18n(
			<StatSummary
				botStatus={EMPTY_BREAKDOWN}
				language="en"
				qrVsRegular={EMPTY_BREAKDOWN}
				totals={TOTALS}
			/>,
		);
		expect(screen.getAllByText('No data yet')).toHaveLength(2);
	});
});
