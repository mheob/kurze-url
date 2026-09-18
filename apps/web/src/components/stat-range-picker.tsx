/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file traces to `StatsWindow` (apps/web/src/lib/stats-window.ts), whose two string properties
   are not marked readonly. That file is Task 3's tested interface, not this task's to edit, and
   its own tests pin its exact shape — adding readonly there is out of scope here. */

import { parseISO } from 'date-fns';
import { useState } from 'react';
import type { DateRange } from 'react-day-picker';
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

export interface StatRangePickerProps {
	/** The active language, for date formatting. */
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
 * @param props - The component's props.
 * @param props.language - The active language, for date formatting.
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
