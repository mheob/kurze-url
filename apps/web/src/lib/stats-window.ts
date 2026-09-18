// The endpoint's own ISO date length: `toIsoDay` slices `toISOString()` down
// to the YYYY-MM-DD prefix.
const ISO_DATE_LENGTH = 10;

// The middle preset, in days. 7 and RETENTION_DAYS need no constant of their
// own — one is a single digit this repo's lint config always ignores, the
// other already has a name.
const PRESET_MONTH_DAYS = 30;

const ISO_DAY = /^\d{4}-\d{2}-\d{2}$/u;

/**
 * @param date - Any instant.
 * @returns Its UTC calendar date as YYYY-MM-DD.
 */
function toIsoDay(date: Readonly<Date>): string {
	return date.toISOString().slice(0, ISO_DATE_LENGTH);
}

/**
 * @param date - The day to move from.
 * @param days - How many days to subtract.
 * @returns The resulting UTC calendar date as YYYY-MM-DD.
 */
function minusDays(date: Readonly<Date>, days: number): string {
	const moved = new Date(date);
	moved.setUTCDate(moved.getUTCDate() - days);
	return toIsoDay(moved);
}

/**
 * The endpoint serves `bucket_start >= today − 89` and deletes anything older,
 * both derived from one constant in `apps/api/internal/api`. Ninety is that
 * window counted inclusively: today plus the eighty-nine days before it.
 */
export const RETENTION_DAYS = 90;

/**
 * The three preset window lengths, in days, in the order they're offered.
 * The single source of truth for "which presets exist" — `matchingPreset`
 * below and `StatRangePicker`'s three buttons both read this array rather
 * than each carrying their own copy of `[7, 30, 90]`, so the two cannot
 * silently disagree if the middle preset ever moves.
 */
export const PRESET_DAYS = [7, PRESET_MONTH_DAYS, RETENTION_DAYS] as const;

/** A resolved window, in the YYYY-MM-DD the endpoint takes and echoes. */
export interface StatsWindow {
	from: string;
	to: string;
}

/** What survives validation of the route's search parameters. */
export interface StatsSearch {
	from?: string;
	to?: string;
}

/**
 * @param today - The current instant; injected so tests do not depend on the clock.
 * @returns The oldest day the endpoint still serves.
 */
export function retentionFloor(today: Readonly<Date>): string {
	return minusDays(today, RETENTION_DAYS - 1);
}

/**
 * @param days - How many days the window should span, today included.
 * @param today - The current instant.
 * @returns The window a preset button stands for.
 */
export function presetWindow(days: number, today: Readonly<Date>): StatsWindow {
	return { from: minusDays(today, days - 1), to: toIsoDay(today) };
}

/**
 * Rejects anything that is not a real calendar date. A rejected value is
 * dropped rather than replaced: absent means "let the endpoint apply its own
 * default", and inventing one here would put the default in two places.
 *
 * `from > to` is deliberately NOT checked. The endpoint answers 422 for that
 * pair and the picker cannot produce it; re-implementing the rule here would
 * make the frontend a second, quieter authority on it.
 *
 * @param search - The raw search parameters, whose values are `unknown` because a URL can carry anything.
 * @returns Only the values that are well-formed calendar dates.
 */
export function parseStatsSearch(search: Readonly<{ from?: unknown; to?: unknown }>): StatsSearch {
	const parsed: StatsSearch = {};
	for (const key of ['from', 'to'] as const) {
		const value = search[key];
		if (typeof value === 'string' && ISO_DAY.test(value)) {
			// `new Date('2026-02-31')` yields 3 March rather than throwing, so the
			// round trip is what rejects a well-shaped impossible date.
			const date = new Date(`${value}T00:00:00Z`);
			if (!Number.isNaN(date.getTime()) && toIsoDay(date) === value) parsed[key] = value;
		}
	}
	return parsed;
}

/**
 * @param window - The window the endpoint reported.
 * @param today - The current instant.
 * @returns The preset that would have produced this window, or undefined for a hand-picked one.
 */
export function matchingPreset(
	window: Readonly<StatsWindow>,
	today: Readonly<Date>,
): number | undefined {
	return PRESET_DAYS.find((days) => {
		const preset = presetWindow(days, today);
		return preset.from === window.from && preset.to === window.to;
	});
}
