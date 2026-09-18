import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import type { StatsWindow } from '../lib/stats-window';
import { StatRecordedJump } from './stat-recorded-jump.tsx';

/**
 * Same pattern as `stat-breakdown-card.test.tsx`: any component that calls
 * `useTranslation` needs an `I18nextProvider` in its tree, or `t(...)` throws
 * looking up `react-i18next`'s default context.
 *
 * @param ui - The element under test.
 * @param language - Which catalogue to load.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactElement` is React's own type; not a declaration this file can edit.
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}

const RECORDED = { from: '2026-06-12', to: '2026-07-03' };

describe(StatRecordedJump, () => {
	it('names the range it would show, formatted for the language', () => {
		renderWithI18n(
			<StatRecordedJump
				language="en"
				onSelect={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				recorded={RECORDED}
			/>,
		);

		expect(
			screen.getByRole('button', { name: 'Show Jun 12, 2026 – Jul 3, 2026' }),
		).toBeInTheDocument();
	});

	it('formats the same range in German', () => {
		renderWithI18n(
			<StatRecordedJump
				language="de"
				onSelect={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				recorded={RECORDED}
			/>,
			'de',
		);

		expect(
			screen.getByRole('button', { name: '12. Juni 2026 – 3. Juli 2026 anzeigen' }),
		).toBeInTheDocument();
	});

	it('hands the recorded range back unchanged when pressed', async () => {
		const onSelect = vi.fn<(window: Readonly<StatsWindow>) => void>();
		renderWithI18n(<StatRecordedJump language="en" onSelect={onSelect} recorded={RECORDED} />);

		await userEvent.click(screen.getByRole('button'));

		// The exact days the API reported, not a window recomputed from them:
		// the API bounded them by the retention floor, and recomputing would
		// throw that away.
		expect(onSelect).toHaveBeenCalledWith({ from: '2026-06-12', to: '2026-07-03' });
	});
});
