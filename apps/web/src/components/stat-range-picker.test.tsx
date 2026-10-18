import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test';

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

/**
 * Each day-of-month shape a day button's name can take, paired with the
 * number the cell shows: every English ordinal suffix, the 11th to 13th that
 * take "th", and the last day of the month. German writes every one of them
 * the same way ("1.", "11.", "30."), but the list is shared so both languages
 * are held to the same days.
 */
const DAY_SHAPES = [
	['2026-09-01', '1'],
	['2026-09-02', '2'],
	['2026-09-03', '3'],
	['2026-09-04', '4'],
	['2026-09-11', '11'],
	['2026-09-12', '12'],
	['2026-09-13', '13'],
	['2026-09-21', '21'],
	['2026-09-22', '22'],
	['2026-09-23', '23'],
	['2026-09-30', '30'],
] as const;

/**
 * The weekday column headers per language, in the order they render, with
 * the name of the trigger that opens the calendar in that language. English
 * weeks start on Sunday and German ones on Monday, which is the locale's own
 * `weekStartsOn`, not a choice this component makes.
 */
const WEEKDAY_HEADERS = [
	{
		abbreviations: ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'],
		language: 'en',
		names: ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'],
		trigger: 'Choose dates',
	},
	{
		abbreviations: ['Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa', 'So'],
		language: 'de',
		names: ['Montag', 'Dienstag', 'Mittwoch', 'Donnerstag', 'Freitag', 'Samstag', 'Sonntag'],
		trigger: 'Zeitraum wählen',
	},
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
		// "Today, Friday, September 18, 2026, selected"), not the bare day
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

	// Residual finding 3: the retention-boundary test above only proves the
	// calendar cannot ask for a window past the retention floor — it says
	// nothing about which days the calendar actually highlights as
	// *selected*, which is the other half of the same bug. A UTC-pinned
	// `Date` for a date-only ISO string renders one calendar day early for
	// anyone west of Greenwich, so this has to run under the negative-offset
	// case above to mean anything; `data-range-start`/`data-range-end` are
	// `CalendarDayButton`'s own attributes (`ui/calendar.tsx`), driven by
	// react-day-picker's range modifiers.
	it('marks both ends of the given window as the selected range start and end', async () => {
		const user = userEvent.setup();
		renderWithI18n(
			<StatRangePicker
				language="en"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={TODAY}
				window={{ from: '2026-09-03', to: '2026-09-11' }}
			/>,
		);

		await user.click(screen.getByRole('button', { name: 'Choose dates' }));

		expect(getDayButton('2026-09-03')).toHaveAttribute('data-range-start', 'true');
		expect(getDayButton('2026-09-11')).toHaveAttribute('data-range-end', 'true');
	});

	// axe-core 4.14 enabled `label-content-name-mismatch` (WCAG 2.5.3, "Label in
	// Name") by default. react-day-picker's own name for a day button reads
	// "Thursday, September 10th, 2026": a word-by-word comparison finds "10th"
	// and never the visible "10", so every day of the grid failed it. The days
	// in `DAY_SHAPES` cover each ordinal suffix, the 11th to 13th that take
	// "th", and the last day of the month.
	it.each(DAY_SHAPES)(
		'holds the visible number of %s in its accessible name as a whole word',
		async (isoDay, visibleNumber) => {
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

			const dayButton = getDayButton(isoDay);
			// What axe compares the name against, pinned so a markup change that
			// adds text to the cell cannot leave this asserting about the wrong word.
			expect(dayButton.textContent).toBe(visibleNumber);
			expect(dayButton).toHaveAccessibleName(new RegExp(String.raw`\b${visibleNumber}\b`, 'u'));
		},
	);

	// The same property from the other side: dropping the ordinal must not
	// drop the markers react-day-picker's default name carries.
	it("keeps the today and selected markers of a day button's name", async () => {
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

		// 18 Sep is both today and the range's last day.
		expect(getDayButton('2026-09-18')).toHaveAccessibleName(
			'Today, Friday, September 18, 2026, selected',
		);
		expect(getDayButton('2026-09-10')).toHaveAccessibleName(
			'Thursday, September 10, 2026, selected',
		);
		// After the range and after today: neither marker.
		expect(getDayButton('2026-09-19')).toHaveAccessibleName('Saturday, September 19, 2026');
	});

	// The German interface's calendar. German writes the day of the month as
	// "30." — an ordinal too, but one whose dot axe's word comparison treats as
	// punctuation rather than as part of the word, unlike English "30th". The
	// pattern pins the German order (day, then month) as well, so an English
	// name that happens to contain the number cannot pass for a German one.
	it.each(DAY_SHAPES)(
		'holds the visible number of %s in its German accessible name as a whole word',
		async (isoDay, visibleNumber) => {
			const user = userEvent.setup();
			renderWithI18n(
				<StatRangePicker
					language="de"
					onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
					today={TODAY}
					window={THIRTY_DAYS}
				/>,
				'de',
			);

			await user.click(screen.getByRole('button', { name: 'Zeitraum wählen' }));

			const dayButton = getDayButton(isoDay);
			expect(dayButton.textContent).toBe(visibleNumber);
			expect(dayButton).toHaveAccessibleName(
				new RegExp(String.raw`\b${visibleNumber}\. September 2026\b`, 'u'),
			);
		},
	);

	it("translates the today and selected markers of a day button's name", async () => {
		const user = userEvent.setup();
		renderWithI18n(
			<StatRangePicker
				language="de"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={TODAY}
				window={THIRTY_DAYS}
			/>,
			'de',
		);

		await user.click(screen.getByRole('button', { name: 'Zeitraum wählen' }));

		// The same three days as the English test above, in the same marker
		// positions react-day-picker's own German locale uses.
		expect(getDayButton('2026-09-18')).toHaveAccessibleName(
			'Heute, Freitag, 18. September 2026, ausgewählt',
		);
		expect(getDayButton('2026-09-10')).toHaveAccessibleName(
			'Donnerstag, 10. September 2026, ausgewählt',
		);
		expect(getDayButton('2026-09-19')).toHaveAccessibleName('Samstag, 19. September 2026');
	});

	// react-day-picker renders the weekday row inside an `aria-hidden` `<thead>`,
	// so no role query reaches it: the cells are read from the DOM instead. A
	// screen reader never hears them, but they are on screen.
	it.each(WEEKDAY_HEADERS)(
		'writes the weekday headers in the $language locale',
		async ({ abbreviations, language, names, trigger }) => {
			const user = userEvent.setup();
			renderWithI18n(
				<StatRangePicker
					language={language}
					onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
					today={TODAY}
					window={THIRTY_DAYS}
				/>,
				language,
			);

			await user.click(screen.getByRole('button', { name: trigger }));

			const headers = [...document.querySelectorAll<HTMLTableCellElement>('table thead th')];
			// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `HTMLTableCellElement` is a DOM lib type, not one this codebase declares.
			expect(headers.map((header) => header.getAttribute('aria-label'))).toStrictEqual(names);
			// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `HTMLTableCellElement` is a DOM lib type, not one this codebase declares.
			expect(headers.map((header) => header.textContent)).toStrictEqual(abbreviations);
		},
	);

	// September is spelled the same in both languages, so the caption is read
	// two months back, where English "July" and German "Juli" differ. The
	// navigation buttons' own names come from the locale too.
	it('captions the month and labels the navigation in German', async () => {
		const user = userEvent.setup();
		renderWithI18n(
			<StatRangePicker
				language="de"
				onChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
				today={TODAY}
				window={THIRTY_DAYS}
			/>,
			'de',
		);

		await user.click(screen.getByRole('button', { name: 'Zeitraum wählen' }));
		await user.click(screen.getByRole('button', { name: 'Zum vorherigen Monat' }));
		await user.click(screen.getByRole('button', { name: 'Zum vorherigen Monat' }));

		expect(screen.getByRole('grid', { name: 'Juli 2026' })).toBeInTheDocument();
		expect(screen.getByText('Juli 2026')).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Zum nächsten Monat' })).toBeInTheDocument();
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
