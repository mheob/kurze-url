import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import type { StatsWindow } from '../lib/stats-window';
import { StatRangePicker } from './stat-range-picker.tsx';

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

const TODAY = new Date('2026-09-18T11:30:00Z');
const THIRTY_DAYS = { from: '2026-08-20', to: '2026-09-18' };

/**
 * @param isoDay - The calendar day to find, as YYYY-MM-DD — react-day-picker's own `data-day`
 * attribute on the cell, ISO-formatted regardless of locale.
 * @returns The day's clickable button element.
 */
function getDayButton(isoDay: string): HTMLElement {
	const button = document.querySelector<HTMLButtonElement>(`[data-day="${isoDay}"] button`);
	if (button === null) throw new Error(`no calendar day button for ${isoDay}`);
	return button;
}

/**
 * Correctness item 2 (final review): every ISO→`Date` conversion this
 * component makes has to round-trip through `isoDayFromCalendarDate` (which
 * reads back *local* year/month/day) regardless of which timezone the
 * process itself is running in. The reproduction that found the bug ran
 * this whole file under `TZ=America/New_York` from the shell; running it
 * again here, inside the suite itself, is what keeps a regression from
 * only failing in a CI job nobody remembers to run under a UTC-negative
 * zone. `vi.stubEnv`/`vi.unstubAllEnvs` — Vitest's own env-mocking pair,
 * already used by `server/teams.test.ts` and `server/supabase.test.ts` —
 * is the mechanism used: Node/V8 re-resolves `Date`'s local components
 * from `process.env.TZ` on every call, with no process restart needed, so
 * stubbing it before each test and restoring it after is enough to run the
 * exact same assertions under a second timezone.
 */
const TIMEZONE_CASES = [
	['the environment default', undefined],
	['America/New_York', 'America/New_York'],
] as const;

describe.each(TIMEZONE_CASES)('the range picker under %s', (_label, timezone) => {
	beforeEach(() => {
		if (timezone !== undefined) vi.stubEnv('TZ', timezone);
	});

	afterEach(() => {
		vi.unstubAllEnvs();
	});

	it('marks the preset that matches the current window', () => {
		renderWithI18n(
			<StatRangePicker
				language="en"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={TODAY}
				window={THIRTY_DAYS}
			/>,
		);
		expect(screen.getByRole('button', { name: '30 days' })).toHaveAttribute('aria-pressed', 'true');
		expect(screen.getByRole('button', { name: '7 days' })).toHaveAttribute('aria-pressed', 'false');
	});

	it('marks no preset for a hand-picked window', () => {
		renderWithI18n(
			<StatRangePicker
				language="en"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={TODAY}
				window={{ from: '2026-09-03', to: '2026-09-11' }}
			/>,
		);
		for (const name of ['7 days', '30 days', '90 days']) {
			expect(screen.getByRole('button', { name })).toHaveAttribute('aria-pressed', 'false');
		}
	});

	it('reports the window a preset stands for', async () => {
		const onChange = vi.fn<(window: Readonly<StatsWindow>) => void>();
		const user = userEvent.setup();
		renderWithI18n(
			<StatRangePicker language="en" onChange={onChange} today={TODAY} window={THIRTY_DAYS} />,
		);

		await user.click(screen.getByRole('button', { name: '7 days' }));

		expect(onChange).toHaveBeenCalledWith({ from: '2026-09-12', to: '2026-09-18' });
	});

	// The window shown is the one the endpoint reported, which is not always
	// the one that was asked for — it clamps to the retention floor silently.
	it('displays the window it was given rather than a preset it inferred', () => {
		renderWithI18n(
			<StatRangePicker
				language="en"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={TODAY}
				window={{ from: '2026-06-21', to: '2026-09-18' }}
			/>,
		);
		// `formatDay` resolves English to `en-US`, which renders "Jun 21, 2026"
		// (month, day, year) — observed by running the formatter directly,
		// rather than the day-month-year order a literal might assume.
		expect(screen.getByText(/Jun 21, 2026/u)).toBeInTheDocument();
	});

	it('states how long statistics are kept', () => {
		renderWithI18n(
			<StatRangePicker
				language="en"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={TODAY}
				window={THIRTY_DAYS}
			/>,
		);
		expect(screen.getByText('Statistics are kept for 90 days.')).toBeInTheDocument();
	});

	it('disables calendar days outside the retention window', async () => {
		// Correctness item 2: the calendar must not be able to ask for a window
		// the endpoint would silently clamp. Opens the popover and checks the
		// boundary days directly, rather than trusting that the `disabled`
		// matcher was wired up correctly.
		//
		// Each day cell is a `<td data-day="YYYY-MM-DD">` (react-day-picker's
		// own attribute, ISO-formatted regardless of locale) wrapping a
		// `<button>` whose accessible name is a full descriptive string (e.g.
		// "Today, Friday, September 18th, 2026, selected"), not the bare day
		// number — so the cell is looked up by `data-day` rather than by role
		// and visible text. `PopoverContent` renders through a portal to
		// `document.body`, outside `render`'s own `container`, so the lookup
		// goes through `document` instead.
		const user = userEvent.setup();
		renderWithI18n(
			<StatRangePicker
				language="en"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={TODAY}
				window={THIRTY_DAYS}
			/>,
		);

		await user.click(screen.getByRole('button', { name: 'Choose dates' }));

		// Today (18 Sep 2026) is enabled; tomorrow (19 Sep 2026), still in the
		// same visible month, is after today and must be disabled.
		expect(document.querySelector('[data-day="2026-09-18"]')).not.toHaveAttribute('data-disabled');
		expect(document.querySelector('[data-day="2026-09-19"]')).toHaveAttribute(
			'data-disabled',
			'true',
		);

		// `retentionFloor(TODAY)` is 21 Jun 2026 (90 days back, inclusive) — the
		// oldest day the endpoint still serves, so it must stay enabled while
		// the day right before it (20 Jun) must not. Three "previous month"
		// clicks reach June from September.
		await user.click(screen.getByRole('button', { name: /previous/iu }));
		await user.click(screen.getByRole('button', { name: /previous/iu }));
		await user.click(screen.getByRole('button', { name: /previous/iu }));

		expect(document.querySelector('[data-day="2026-06-20"]')).toHaveAttribute(
			'data-disabled',
			'true',
		);
		expect(document.querySelector('[data-day="2026-06-21"]')).not.toHaveAttribute('data-disabled');
	});

	it('reports two freshly clicked dates as the new range, not a nudge of the old one', async () => {
		// Critical fix: `selected` used to always mirror the current `window`
		// unconditionally, and `window` is always a *complete* range — so
		// react-day-picker's own `addToRange` never started a fresh selection.
		// It nudged whichever endpoint of the OLD range was nearer the click,
		// which both fired `onChange` on a single click (never landing on a
		// half-finished range) and made a second click land on some
		// combination of the newly clicked date and the untouched old
		// endpoint, rather than the two dates actually clicked. Both clicked
		// dates (5 and 10 Sep) stay inside the visible month and well inside
		// the retention window, so no month navigation or disabled-date edge
		// is in play here — only the fresh-selection behaviour under test.
		const onChange = vi.fn<(window: Readonly<StatsWindow>) => void>();
		const user = userEvent.setup();
		renderWithI18n(
			<StatRangePicker language="en" onChange={onChange} today={TODAY} window={THIRTY_DAYS} />,
		);

		await user.click(screen.getByRole('button', { name: 'Choose dates' }));

		await user.click(getDayButton('2026-09-10'));
		// A half-finished range — one endpoint clicked, the other not yet
		// chosen — must never reach `onChange`.
		expect(onChange).not.toHaveBeenCalled();

		await user.click(getDayButton('2026-09-05'));

		expect(onChange).toHaveBeenCalledExactlyOnceWith({ from: '2026-09-05', to: '2026-09-10' });
	});

	it('leaves the window unchanged when the popover closes after only one click', async () => {
		// The other half of the same property: a half-finished pick that never
		// completes must not have mutated anything the caller can see.
		const onChange = vi.fn<(window: Readonly<StatsWindow>) => void>();
		const user = userEvent.setup();
		renderWithI18n(
			<StatRangePicker language="en" onChange={onChange} today={TODAY} window={THIRTY_DAYS} />,
		);

		await user.click(screen.getByRole('button', { name: 'Choose dates' }));
		await user.click(getDayButton('2026-09-05'));
		// Close the popover without a second click (Escape closes it without
		// requiring a real outside-click target).
		await user.keyboard('{Escape}');

		expect(onChange).not.toHaveBeenCalled();
	});
});
