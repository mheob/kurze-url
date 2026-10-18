/* oxlint-disable typescript/prefer-readonly-parameter-types -- the findings of this rule in this
   file trace to two causes. The first is `StatsWindow` (apps/web/src/lib/stats-window.ts),
   whose two string properties are not marked readonly. That file is Task 3's tested interface,
   not this task's to edit, and its own tests pin its exact shape — adding readonly there is out
   of scope here. The second is the parameters of the `labelDayButton` that `dayButtonLabeller`
   returns, whose types react-day-picker's `Labels` type dictates, so they cannot be made
   readonly. */

import { parseISO } from 'date-fns';
import { useMemo, useState } from 'react';
import {
	DateLib,
	type DateLibOptions,
	type DateRange,
	type Labels,
	type Locale,
	type Modifiers,
} from 'react-day-picker';
import { de } from 'react-day-picker/locale/de';
import { enUS } from 'react-day-picker/locale/en-US';
import { useTranslation } from 'react-i18next';

import { formatDay } from '../lib/format';
import type { Language } from '../lib/preferences';
import {
	matchingPreset,
	PRESET_DAYS,
	presetWindow,
	retentionFloor,
	type StatsWindow,
} from '../lib/stats-window';
import { Button } from './ui/button';
import { Calendar } from './ui/calendar';
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from './ui/popover';

/** Read back from a calendar-cell `Date`, not `.toISOString()`'s UTC slice. */
const ISO_PAD_LENGTH = 2;

/**
 * The three preset shortcuts, in the order they render, paired with their
 * translation keys. Module scope, not inside the component: the array is
 * the same on every render, and defining it inline would give `.map` a
 * fresh array (and fresh translation-key strings) each time for no benefit.
 *
 * The `days` values themselves come from `stats-window.ts`'s exported
 * `PRESET_DAYS` — the one place that list is written down — rather than a
 * second `[7, 30, 90]` here that could silently drift from
 * `matchingPreset`'s own copy if the middle preset ever moved. Destructured
 * positionally rather than zipped with `.map`, so neither array's length
 * has to be trusted against the other's at the type level.
 */
const [PRESET_SHORT_DAYS, PRESET_MONTH_DAYS, PRESET_RETENTION_DAYS] = PRESET_DAYS;
const PRESETS = [
	{ days: PRESET_SHORT_DAYS, labelKey: 'stats.preset7' },
	{ days: PRESET_MONTH_DAYS, labelKey: 'stats.preset30' },
	{ days: PRESET_RETENTION_DAYS, labelKey: 'stats.preset90' },
] as const;

/**
 * `react-day-picker` builds each calendar cell's `Date` from local
 * year/month/day components (see `ui/calendar.tsx`'s `CalendarDayButton`,
 * which reads `day.date` the same way), not from a UTC instant. Reading it
 * back through `.toISOString()` — the way `stats-window.ts`'s own
 * `toIsoDay` does for its UTC-pinned inputs — would shift the date by the
 * runtime's UTC offset for anyone west of Greenwich. Reading the same local
 * components back out is what makes this the inverse of how the cell's
 * `Date` was built, regardless of timezone.
 *
 * `parseISO` (below, from `date-fns`) is this function's own inverse: it
 * turns a date-only ISO string into a `Date` at *local* midnight for that
 * calendar day, the same local components this function reads back out — so
 * `isoDayFromCalendarDate(parseISO(day)) === day` in any timezone. The two
 * used to disagree: every ISO→`Date` conversion below built a UTC instant
 * instead (`new Date(`${day}T00:00:00Z`)`), which this function's own read
 * then shifts by the runtime's UTC offset — one calendar day earlier for
 * anyone west of Greenwich.
 *
 * @param date - A calendar day as `Calendar`'s `onSelect` reports it.
 * @returns The same calendar day as YYYY-MM-DD.
 */
function isoDayFromCalendarDate(date: Readonly<Date>): string {
	const year = date.getFullYear();
	const month = String(date.getMonth() + 1).padStart(ISO_PAD_LENGTH, '0');
	const day = String(date.getDate()).padStart(ISO_PAD_LENGTH, '0');
	return `${year}-${month}-${day}`;
}

/**
 * The calendar's locale per language: react-day-picker's own locales, not
 * date-fns' bare ones. Each is the date-fns locale (month and weekday names,
 * the week starting on Monday in German) plus the library's own translations
 * of every label it renders — the previous/next buttons, the grid, the
 * weekday headers — so this file overrides only the one label it has to,
 * `labelDayButton` below. A date-fns locale alone would translate the dates
 * and leave those labels English.
 *
 * Imported one locale per subpath, never through the `react-day-picker/locale`
 * barrel. The server bundle keeps react-day-picker external, so Node runs
 * that barrel as written: it loads all 95 of the library's locale modules,
 * and the 531 date-fns files behind them, to use two locales.
 */
const CALENDAR_LOCALES: Readonly<Record<Language, Locale>> = { de, en: enUS };

/**
 * The date-fns pattern a day button's name is written in, per language.
 *
 * English is react-day-picker's own `PPPP` with one difference: the day of
 * the month is written `30`, not `30th`. The cell's visible text is the bare
 * number, and axe-core 4.14 turned `label-content-name-mismatch` (WCAG 2.5.3,
 * "Label in Name") on by default: it compares word by word, so the library's
 * "Sunday, August 30th, 2026" holds the word `30th` and never `30`, and every
 * day of the grid failed it.
 *
 * German is `PPPP` unchanged, "Sonntag, 30. August 2026". The German ordinal
 * is the number and a dot, and axe turns every character that is neither a
 * letter nor a digit into a space before it splits the name into words, so
 * `30.` yields the word `30` and the rule passes. The `CalendarOpenGerman`
 * story runs it against the whole grid.
 */
const DAY_BUTTON_PATTERNS: Readonly<Record<Language, string>> = {
	de: 'PPPP',
	en: 'EEEE, MMMM d, y',
};

/**
 * Builds react-day-picker's `labelDayButton` for one language. It writes the
 * date in that language's pattern, then adds the today marker in front and
 * the selected marker behind, in the order react-day-picker's own English and
 * German locales use, so a screen reader hears what it always did. The
 * markers are passed in rather than written here because they are copy: they
 * come from the translation catalogue, and the date from the calendar's own
 * locale, which `DayPicker` hands over as `dateLib`.
 *
 * @param pattern - The date-fns pattern for the date, from `DAY_BUTTON_PATTERNS`.
 * @param todayMarker - The word that marks today, already translated.
 * @param selectedMarker - The word that marks a selected day, already translated.
 * @returns The label function `Calendar`'s `labels` prop takes.
 */
function dayButtonLabeller(
	pattern: string,
	todayMarker: string,
	selectedMarker: string,
): Labels['labelDayButton'] {
	// oxlint-disable-next-line max-params -- react-day-picker's `Labels['labelDayButton']` is `(date, modifiers, options, dateLib)`; a function with fewer parameters would drop `dateLib`, which `DayPicker` hands over so each label does not build its own.
	return (date: Date, modifiers: Modifiers, options?: DateLibOptions, dateLib?: DateLib) => {
		let label = (dateLib ?? new DateLib(options)).format(date, pattern);
		if (modifiers.today) label = `${todayMarker}, ${label}`;
		if (modifiers.selected) label = `${label}, ${selectedMarker}`;
		return label;
	};
}

export interface StatRangePickerProps {
	/** The active language, for date formatting and the calendar's locale. */
	readonly language: Language;
	/** Called with the newly chosen window, from a preset or a completed calendar range. */
	readonly onChange: (window: StatsWindow) => void;
	/** The current instant; injected rather than read from the clock so the component is testable. */
	readonly today: Date;
	/**
	 * The window the **response** reported, not the one that was requested —
	 * the endpoint clamps silently to the retention floor, and rendering an
	 * inferred preset instead of these bounds would show a range the data
	 * does not cover.
	 */
	readonly window: StatsWindow;
}

/**
 * The time-window picker for a link's statistics page: three mutually
 * exclusive preset shortcuts plus a calendar for a hand-picked range.
 *
 * The presets use `aria-pressed`, not a `Toggle`/toggle-group component —
 * none is installed, and these are shortcuts for a value the caller owns,
 * not independent on/off switches. `matchingPreset` (not this component)
 * decides which one, if any, is pressed, so a hand-picked range that
 * happens to equal a preset's bounds is still reported as that preset
 * (see its own docstring) — this component never second-guesses it.
 *
 * The calendar's `disabled` matcher keeps it from ever producing a window
 * the endpoint would clamp: without it, picking a date before the
 * retention floor would silently come back shorter than asked for, with
 * nothing here to explain why. Its `onSelect` only calls `onChange` once
 * both ends of a range are chosen — react-day-picker reports a
 * half-finished range with `to` left `undefined`, and forwarding that
 * would make `from` and `to` disagree.
 *
 * The calendar's `selected` is `window` only until the reader clicks
 * inside it; from the first click on, it is `pendingRange`, this
 * component's own state, not a prop. `window` is always a *complete*
 * range, and if `selected` mirrored it on every render, react-day-picker's
 * own `addToRange` would never see an incomplete range to build on — every
 * single click would land on a "complete range" branch that nudges
 * whichever endpoint of the *old* window is nearer the click, firing
 * `onChange` immediately and never accumulating the two dates actually
 * clicked. `resetOnSelect` is what makes the first click, made against a
 * complete range, start a brand-new one instead of nudging it; buffering
 * that new (initially incomplete) range in `pendingRange` is what lets the
 * *second* click build on the first rather than re-deriving from the
 * stale, still-complete `window` prop again. The popover's `onOpenChange`
 * clears the buffer on every open and close, so a half-finished pick left
 * behind by a closed popover cannot resurface the next time it opens.
 *
 * The calendar speaks the interface's language. Its locale and the pattern
 * of each day button's name follow the `language` prop, the same value that
 * `formatDay` formats the summary with, while the today and selected markers
 * in that name come from the catalogue through `t`. The app sets both from
 * the one language preference, so they agree.
 *
 * @param props - The component's props.
 * @param props.language - The active language, for date formatting and the calendar's locale.
 * @param props.onChange - Called with the newly chosen window.
 * @param props.today - The current instant, injected for testability.
 * @param props.window - The window the response reported.
 * @returns The rendered picker.
 */
export function StatRangePicker({
	language,
	onChange,
	today,
	window,
}: StatRangePickerProps): React.JSX.Element {
	const { t } = useTranslation();
	const activePreset = matchingPreset(window, today);
	const [pendingRange, setPendingRange] = useState<DateRange | undefined>(undefined);

	// `labels` is one of `DayPicker`'s own `useMemo` dependencies, so it stays
	// the same object across renders, as the module-level constant it replaced
	// did. The memo's keys are the language and the two markers, plain strings
	// that change only when the language does.
	const todayMarker = t('stats.calendarToday');
	const selectedMarker = t('stats.calendarSelected');
	const calendarLabels = useMemo(
		() => ({
			labelDayButton: dayButtonLabeller(DAY_BUTTON_PATTERNS[language], todayMarker, selectedMarker),
		}),
		[language, todayMarker, selectedMarker],
	);

	const selectedRange: DateRange = pendingRange ?? {
		from: parseISO(window.from),
		to: parseISO(window.to),
	};

	return (
		<div>
			{/* A native `<fieldset>` carries the implicit ARIA role "group" on its
			    own, the same reasoning `LanguageSwitcher` uses for its own
			    mutually-exclusive button row, so this needs no bolted-on
			    `role="group"` to satisfy `jsx-a11y/prefer-tag-over-role`. */}
			<fieldset aria-label={t('stats.rangeLabel')}>
				{PRESETS.map(({ days, labelKey }) => (
					<Button
						aria-pressed={activePreset === days}
						key={days}
						onClick={() => {
							onChange(presetWindow(days, today));
						}}
						type="button"
						variant="outline"
					>
						{t(labelKey)}
					</Button>
				))}

				<Popover
					onOpenChange={() => {
						// Discard any in-progress pick, in either direction: reopening
						// after a half-finished selection must show the actual
						// (unchanged) `window`, not resurrect a stale single-ended one.
						setPendingRange(undefined);
					}}
				>
					{/* oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- Base UI's `render`-prop
					    composition idiom (`useRender`'s "Migrating from Radix UI" guide): this is the
					    element `PopoverTrigger` clones and merges its own props onto. A stable reference
					    would need a `useMemo` around a one-line static element per picker instance. */}
					<PopoverTrigger render={<Button type="button" variant="outline" />}>
						{t('stats.customRange')}
					</PopoverTrigger>
					<PopoverContent>
						{/* Base UI wires the popup's `aria-labelledby` to this title's own
						    id automatically (the same mechanism `AlertDialogTitle` uses),
						    so the open popover — role `dialog` — has a real accessible
						    name instead of none. Reusing the trigger's own translation key
						    rather than adding a new one: the two describe the same thing. */}
						<PopoverTitle>{t('stats.customRange')}</PopoverTitle>
						<Calendar
							disabled={{ after: today, before: parseISO(retentionFloor(today)) }}
							labels={calendarLabels}
							locale={CALENDAR_LOCALES[language]}
							mode="range"
							onSelect={(range) => {
								setPendingRange(range);
								if (range?.from !== undefined && range.to !== undefined) {
									onChange({
										from: isoDayFromCalendarDate(range.from),
										to: isoDayFromCalendarDate(range.to),
									});
								}
							}}
							resetOnSelect
							selected={selectedRange}
							// Without it the calendar takes its visible month and its
							// "today" marker from the system clock rather than from the
							// instant this component was given, so the two disagree as
							// soon as they fall in different months.
							today={today}
						/>
					</PopoverContent>
				</Popover>

				<span>
					{t('stats.rangeSummary', {
						from: formatDay(window.from, language),
						to: formatDay(window.to, language),
					})}
				</span>
			</fieldset>

			<p>{t('stats.rangeRetentionNote')}</p>
		</div>
	);
}
