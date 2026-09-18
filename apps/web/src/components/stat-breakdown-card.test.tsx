import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import { StatBreakdownCard } from './stat-breakdown-card.tsx';

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

const FULL = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: [
		{ clicks: 80, unique_visitors: 60, value: 'Chrome' },
		{ clicks: 20, unique_visitors: 15, value: 'Firefox' },
	],
};

describe(StatBreakdownCard, () => {
	it('lists every value with its click count', () => {
		renderWithI18n(<StatBreakdownCard breakdown={FULL} language="en" title="Browser" />);
		expect(screen.getByText('Chrome')).toBeInTheDocument();
		expect(screen.getByText('80')).toBeInTheDocument();
	});

	// Without this row a list capped at ten silently misstates its own
	// dimension's total, which is the whole reason the API returns other_*.
	it('reports the values it left out', () => {
		renderWithI18n(
			<StatBreakdownCard
				breakdown={{ ...FULL, other_clicks: 7, other_unique_visitors: 5, other_values: 3 }}
				language="en"
				title="Referrer"
			/>,
		);
		expect(screen.getByText('3 further values')).toBeInTheDocument();
	});

	it('omits the row entirely when nothing was left out', () => {
		renderWithI18n(<StatBreakdownCard breakdown={FULL} language="en" title="Browser" />);
		expect(screen.queryByText(/further value/u)).not.toBeInTheDocument();
	});

	it('uses the singular for exactly one further value', () => {
		renderWithI18n(
			<StatBreakdownCard
				breakdown={{ ...FULL, other_clicks: 2, other_unique_visitors: 1, other_values: 1 }}
				language="en"
				title="Browser"
			/>,
		);
		expect(screen.getByText('1 further value')).toBeInTheDocument();
	});

	it('says so when the dimension recorded nothing', () => {
		renderWithI18n(
			<StatBreakdownCard
				breakdown={{ other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null }}
				language="en"
				title="Country"
			/>,
		);
		expect(screen.getByText('Nothing recorded in this window')).toBeInTheDocument();
	});

	// referrer and utm_source are attacker-supplied text, truncated to 128
	// bytes by the API and otherwise arbitrary. They render as text, never as
	// something the page will fetch or link to.
	it('renders a URL-shaped value as plain text', () => {
		renderWithI18n(
			<StatBreakdownCard
				breakdown={{
					other_clicks: 0,
					other_unique_visitors: 0,
					other_values: 0,
					values: [{ clicks: 1, unique_visitors: 1, value: 'https://evil.test/x' }],
				}}
				language="en"
				title="Referrer"
			/>,
		);
		expect(screen.getByText('https://evil.test/x')).toBeInTheDocument();
		expect(screen.queryByRole('link')).not.toBeInTheDocument();
	});
});
