/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file traces to `StatsWindow` (apps/web/src/lib/stats-window.ts), whose two string properties
   are not marked readonly. That file is Task 3's tested interface, not this task's to edit, and
   its own tests pin its exact shape — adding readonly there is out of scope here. */

import { useTranslation } from 'react-i18next';

import { formatDay } from '../lib/format';
import type { Language } from '../lib/preferences';
import {
	matchingPreset,
	presetWindow,
	retentionFloor,
	RETENTION_DAYS,
	type StatsWindow,
} from '../lib/stats-window';
import { Button } from './ui/button';
import { Calendar } from './ui/calendar';
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from './ui/popover';

/** The middle preset, in days. See `stats-window.ts` for why 7 and `RETENTION_DAYS` need no constant of their own. */
const PRESET_MONTH_DAYS = 30;

/** Read back from a calendar-cell `Date`, not `.toISOString()`'s UTC slice. */
const ISO_PAD_LENGTH = 2;

/**
 * The three preset shortcuts, in the order they render. Module scope, not
 * inside the component: the array is the same on every render, and defining
 * it inline would give `.map` a fresh array (and fresh translation-key
 * strings) each time for no benefit.
 */
const PRESETS = [
	{ days: 7, labelKey: 'stats.preset7' },
	{ days: PRESET_MONTH_DAYS, labelKey: 'stats.preset30' },
	{ days: RETENTION_DAYS, labelKey: 'stats.preset90' },
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

				<Popover>
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
							disabled={{ after: today, before: new Date(`${retentionFloor(today)}T00:00:00Z`) }}
							mode="range"
							onSelect={(range) => {
								if (range?.from !== undefined && range.to !== undefined) {
									onChange({
										from: isoDayFromCalendarDate(range.from),
										to: isoDayFromCalendarDate(range.to),
									});
								}
							}}
							selected={{
								from: new Date(`${window.from}T00:00:00Z`),
								to: new Date(`${window.to}T00:00:00Z`),
							}}
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
