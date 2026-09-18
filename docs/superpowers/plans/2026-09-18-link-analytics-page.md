# Link Analytics Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A team member can open a link and see what it did — totals, a daily series, and six dimension breakdowns — over a window they choose.

**Architecture:** One new route under the existing `_authed` layout, with the window in its search parameters so it is bookmarkable. The loader reads `GET /v1/links/{link_id}/stats` through a new server function and React Query, the same way the link list reads its own endpoint. Presentation splits into four components that each take plain data and render it, so each is testable without a router.

**Tech Stack:** TanStack Start/Router/Query, shadcn/ui on Base UI (`base-sera`), shadcn `chart` on Recharts 3.8.0, react-day-picker, date-fns, Vitest + RTL, Storybook, axe-core.

**Spec:** `docs/superpowers/specs/2026-09-18-link-analytics-page-design.md`

## Global Constraints

Copied from the spec. Every task's requirements implicitly include these.

- **i18n from the first component.** No hardcoded user-facing string, including chart labels, axis ticks, empty states and the accessible names of every control. Keys go into both `apps/web/src/i18n/locales/en.json` and `de.json`, in the same shape.
- **Accessibility is a requirement** (WCAG 2.1 AA), checked in CI at two levels.
- **Generated components under `apps/web/src/components/ui/` are never hand-edited.** Customisation lives in a wrapper, in tokens, or in the preset. They are excluded from lint and format via `generated.config.ts`.
- **Colour tokens live under `[data-theme='indigo']`**, not on bare `:root`. `:root` keeps only what is not colour.
- **Colour never carries meaning alone.** In the series chart, colour says which metric and stroke style says which population.
- **The page displays the window from the response, never from the URL.** The endpoint clamps silently to the 90-day retention window.
- **British spelling in prose** (colour, behaviour, recognise). `codebook.toml` declares `en_us`; that is a known misconfiguration, and editor-style writes may Americanise. Check prose after writing it.
- **Conventional Commits**, subject capped at **50 characters including type and scope**. No co-author or generator footer, ever.
- **Run `pnpm format` before every commit.** Never bypass the Lefthook hooks.
- **All git writes go through GitButler** (`but commit -b <branch> -m …`), never `git add`/`git commit`/`git checkout`.
- **Commit `apps/web/src/routeTree.gen.ts` only where required.** This plan adds a route, so Task 10 requires it.
- **The branch is `feat/link-analytics-page`**, which already exists and carries the spec commit.

## File Structure

| File | Responsibility | Task |
| --- | --- | --- |
| `apps/web/src/styles/app.css` | Theme-aware chart ramp | 1 |
| `apps/web/src/styles/chart-contrast.test.ts` | Guards every ramp step against its own `--card` | 1 |
| `docs/superpowers/plans/2026-09-18-analytics-contrast.md` | The measurements, including the pre-fix failures | 1 |
| `apps/web/src/lib/format.ts` | `formatCount`, `formatDay` | 2 |
| `apps/web/src/lib/stats-window.ts` | Presets, search parsing, the retention floor | 3 |
| `apps/web/src/server/links.ts` | `getLinkStatsFor`, `getLinkStatsFn`, `linkStatsQueryOptions` | 4 |
| `apps/web/src/components/ui/{chart,calendar,popover}.tsx` | Generated, never edited | 5 |
| `apps/web/src/components/stat-summary.tsx` | Four totals plus the two binary splits | 6 |
| `apps/web/src/components/stat-series-chart.tsx` | Line chart, bot toggle, hidden data table | 7 |
| `apps/web/src/components/stat-breakdown-card.tsx` | One dimension's list plus its "other" row | 8 |
| `apps/web/src/components/stat-range-picker.tsx` | Presets plus the calendar popover | 9 |
| `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.stats.tsx` | Route, search validation, loader, layout, error component | 10 |
| `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx` | Gains a link to the statistics page | 10 |
| `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.stats.a11y.test.tsx` | axe over the composed page | 11 |

Each presentation component takes plain data and renders it. None of them reads the router, so each is testable and storyable without one; the route is the only file that knows about search parameters.

---

### Task 1: The theme-aware chart ramp

The five `--chart-*` tokens currently carry identical values in both the light and the dark block of `app.css`. Charts render inside a `Card`, so the background is `--card`. Measured against it, `--chart-1` fails WCAG 1.4.11's 3:1 floor in light mode and `--chart-3`, `--chart-4` and `--chart-5` fail in dark mode. This task fixes that and leaves behind a test that will not let it happen again.

**Files:**

- Modify: `apps/web/src/styles/app.css` (the `--chart-*` lines in both the `[data-theme='indigo']` and the `[data-theme='indigo'].dark` block)
- Create: `apps/web/src/styles/chart-contrast.test.ts`
- Create: `docs/superpowers/plans/2026-09-18-analytics-contrast.md`

**Interfaces:**

- Consumes: nothing.
- Produces: `--chart-1` through `--chart-5`, each clearing 3:1 against its own theme's `--card`. Later tasks use `--chart-1` for the clicks line and the share bars, and `--chart-5` for the unique-visitors line.

- [ ] **Step 1: Write the failing test**

Create `apps/web/src/styles/chart-contrast.test.ts`. It reads the stylesheet rather than importing a constant, so it fails if someone edits the CSS and not a fixture.

```ts
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

/**
 * WCAG 1.4.11 asks for 3:1 between a graphical object and its background. A
 * chart line is a graphical object, and the chart sits inside a Card, so the
 * background is --card and not --background.
 */
const MINIMUM_CONTRAST = 3;

const css = readFileSync(fileURLToPath(new URL('./app.css', import.meta.url)), 'utf8');

/**
 * @param l - OKLCH lightness, 0 to 1.
 * @param c - OKLCH chroma.
 * @param hDegrees - OKLCH hue in degrees.
 * @returns Linear sRGB, unclamped, so an out-of-gamut colour is visible as a component outside 0..1.
 */
function oklchToLinearSrgb(l: number, c: number, hDegrees: number): readonly number[] {
	const h = (hDegrees * Math.PI) / 180;
	const a = c * Math.cos(h);
	const b = c * Math.sin(h);
	const lCube = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3;
	const mCube = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3;
	const sCube = (l - 0.0894841775 * a - 1.291485548 * b) ** 3;
	return [
		4.0767416621 * lCube - 3.3077115913 * mCube + 0.2309699292 * sCube,
		-1.2684380046 * lCube + 2.6097574011 * mCube - 0.3413193965 * sCube,
		-0.0041960863 * lCube - 0.7034186147 * mCube + 1.707614701 * sCube,
	];
}

/**
 * @param colour - An OKLCH triple.
 * @returns Its WCAG relative luminance, with each channel clamped into sRGB the way a browser paints it.
 */
function luminance(colour: readonly [number, number, number]): number {
	const [r, g, b] = oklchToLinearSrgb(colour[0], colour[1], colour[2]).map((v) =>
		Math.min(1, Math.max(0, v)),
	);
	return 0.2126 * (r ?? 0) + 0.7152 * (g ?? 0) + 0.0722 * (b ?? 0);
}

/**
 * @param a - First colour.
 * @param b - Second colour.
 * @returns The WCAG contrast ratio between them.
 */
function contrast(
	a: readonly [number, number, number],
	b: readonly [number, number, number],
): number {
	const [high, low] = [luminance(a), luminance(b)].sort((x, y) => y - x);
	return ((high ?? 0) + 0.05) / ((low ?? 0) + 0.05);
}

/**
 * Reads one token out of one CSS block. The blocks are found by their
 * selector rather than by line number so the test survives the file moving
 * around.
 *
 * @param selector - The exact selector text, e.g. `[data-theme='indigo']`.
 * @param token - The custom property name without its leading dashes, e.g. `chart-1`.
 * @returns The OKLCH triple the block assigns to that token.
 */
function tokenIn(selector: string, token: string): [number, number, number] {
	const blockStart = css.indexOf(selector);
	expect(blockStart, `selector ${selector} not found in app.css`).toBeGreaterThanOrEqual(0);
	const block = css.slice(blockStart, css.indexOf('\n}', blockStart));
	const match = new RegExp(
		`--${token}:\\s*oklch\\(([\\d.]+)\\s+([\\d.]+)\\s+([\\d.]+)\\)`,
		'u',
	).exec(block);
	expect(match, `--${token} not found in ${selector}`).not.toBeNull();
	return [Number(match?.[1]), Number(match?.[2]), Number(match?.[3])];
}

describe('the indigo chart ramp', () => {
	const steps = [1, 2, 3, 4, 5];

	it.each(steps)('step %i clears the contrast floor against the light card', (step) => {
		const ratio = contrast(tokenIn("[data-theme='indigo']", `chart-${step}`), [1, 0, 0]);
		expect(ratio).toBeGreaterThanOrEqual(MINIMUM_CONTRAST);
	});

	it.each(steps)('step %i clears the contrast floor against the dark card', (step) => {
		const ratio = contrast(tokenIn("[data-theme='indigo'].dark", `chart-${step}`), [0.205, 0, 0]);
		expect(ratio).toBeGreaterThanOrEqual(MINIMUM_CONTRAST);
	});

	// The two series the chart draws by default. They are the widest pair the
	// ramp offers and they are still only ~3:1 apart, which is why the chart
	// also distinguishes them by stroke style. This test pins the pairing: if
	// someone renumbers the ramp, the chart must be renumbered with it.
	it.each(["[data-theme='indigo']", "[data-theme='indigo'].dark"])(
		'separates the two default series in %s',
		(selector) => {
			const ratio = contrast(tokenIn(selector, 'chart-1'), tokenIn(selector, 'chart-5'));
			expect(ratio).toBeGreaterThanOrEqual(3);
		},
	);
});
```

- [ ] **Step 2: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- chart-contrast
```

Expected: red. Light `chart-1` reports about `2.009` and dark `chart-3`, `chart-4` and `chart-5` report about `2.781`, `2.221` and `1.776`. Record those numbers — Step 5 writes them down as the permanent record of what was wrong.

- [ ] **Step 3: Replace the ramp in both blocks**

In `apps/web/src/styles/app.css`, inside `[data-theme='indigo']`:

```css
/* Each step clears WCAG 1.4.11's 3:1 against --card in this theme, and
	   every value is inside the sRGB gamut — chroma is 92% of the maximum
	   this hue admits at that lightness, so the painted colour is the
	   specified one rather than a silently clamped neighbour. The light ramp
	   runs dark-to-light-limited: nothing above L 0.648 can clear white. */
--chart-1: oklch(0.648 0.175 277);
--chart-2: oklch(0.585 0.211 277);
--chart-3: oklch(0.511 0.256 277);
--chart-4: oklch(0.457 0.26 277);
--chart-5: oklch(0.398 0.227 277);
```

And inside `[data-theme='indigo'].dark`:

```css
/* The dark ramp is not the light one reused: it runs the other way,
	   because nothing below L 0.585 clears the dark card. This block existed
	   before with the light values copied into it verbatim, which is how
	   three of the five steps shipped below the contrast floor. */
--chart-1: oklch(0.585 0.211 277);
--chart-2: oklch(0.66 0.168 277);
--chart-3: oklch(0.732 0.129 277);
--chart-4: oklch(0.806 0.09 277);
--chart-5: oklch(0.88 0.054 277);
```

- [ ] **Step 4: Run the test and watch it pass**

```bash
pnpm --filter @kurze-url/web test -- chart-contrast
```

Expected: 12 passing. The measured ratios against `--card` are 3.406 / 4.491 / 6.408 / 8.223 / 10.369 in light, and 3.989 / 5.532 / 7.368 / 9.662 / 12.404 in dark.

- [ ] **Step 5: Write the contrast record**

Create `docs/superpowers/plans/2026-09-18-analytics-contrast.md` holding a before-and-after table for all ten values, the two default-series pairings (3.045 light, 3.110 dark), and one paragraph explaining that a single hue cannot separate four series, which is why the chart uses stroke style as a second channel. Keep the failing numbers: a record that shows only the fixed state cannot tell a future reader what the trap was.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "fix(web): make the chart ramp theme-aware" <ids from but diff>
```

---

### Task 2: Number and date formatting

Nothing in this repository formats a number or a date for a locale; a grep for `Intl` finds nothing, so a German reader currently sees `1234` and `2026-09-15`. A page made entirely of numbers and dates is where that has to change.

**Files:**

- Create: `apps/web/src/lib/format.ts`
- Create: `apps/web/src/lib/format.test.ts`

**Interfaces:**

- Consumes: the `Language` type from `apps/web/src/lib/preferences.ts`.
- Produces: `formatCount(value: number, language: Language): string` and `formatDay(isoDate: string, language: Language): string`. Tasks 6, 7, 8 and 9 use both.

- [ ] **Step 1: Write the failing test**

Create `apps/web/src/lib/format.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { formatCount, formatDay } from './format.ts';

describe(formatCount, () => {
	it('groups thousands the English way', () => {
		expect(formatCount(1234567, 'en')).toBe('1,234,567');
	});

	it('groups thousands the German way', () => {
		expect(formatCount(1234567, 'de')).toBe('1.234.567');
	});

	it('leaves a small number alone in both languages', () => {
		expect(formatCount(7, 'en')).toBe('7');
		expect(formatCount(7, 'de')).toBe('7');
	});

	it('renders zero rather than an empty string', () => {
		expect(formatCount(0, 'en')).toBe('0');
	});
});

describe(formatDay, () => {
	it('renders a day the English way', () => {
		expect(formatDay('2026-09-15', 'en')).toBe('15 Sept 2026');
	});

	it('renders a day the German way', () => {
		expect(formatDay('2026-09-15', 'de')).toBe('15. Sept. 2026');
	});

	// The API sends a plain calendar date. Parsing it as local time would shift
	// it by a day for anyone west of UTC, which would silently relabel every
	// point on the chart.
	it('does not shift the date across a timezone boundary', () => {
		expect(formatDay('2026-01-01', 'en')).toContain('2026');
		expect(formatDay('2026-01-01', 'en')).toContain('1');
	});
});
```

- [ ] **Step 2: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- format
```

Expected: FAIL, "Failed to resolve import ./format.ts".

- [ ] **Step 3: Write the implementation**

Create `apps/web/src/lib/format.ts`:

```ts
import type { Language } from './preferences.ts';

/**
 * Both formatters take the language as an argument rather than reading a
 * global. The app renders on the server and hydrates in the browser, and the
 * two must agree exactly or React reports a hydration mismatch — passing the
 * value that already travels through the preferences cookie is what makes
 * them agree by construction.
 *
 * The formatters are memoised because constructing an Intl formatter is the
 * expensive part and a breakdown card builds one per row otherwise.
 */
const counts = new Map<Language, Intl.NumberFormat>();
const days = new Map<Language, Intl.DateTimeFormat>();

/**
 * @param value - A whole number of clicks or visitors.
 * @param language - The active language.
 * @returns The number with the language's own thousands grouping.
 */
export function formatCount(value: number, language: Language): string {
	let formatter = counts.get(language);
	if (formatter === undefined) {
		formatter = new Intl.NumberFormat(language);
		counts.set(language, formatter);
	}
	return formatter.format(value);
}

/**
 * @param isoDate - A calendar date as YYYY-MM-DD, exactly as the API sends it.
 * @param language - The active language.
 * @returns The date in the language's medium form.
 */
export function formatDay(isoDate: string, language: Language): string {
	let formatter = days.get(language);
	if (formatter === undefined) {
		// timeZone: 'UTC' is load-bearing. `new Date('2026-01-01')` is midnight
		// UTC, and formatting that in a negative offset renders 31 December —
		// every point on the chart would be labelled with the wrong day for
		// anyone west of Greenwich.
		formatter = new Intl.DateTimeFormat(language, {
			day: 'numeric',
			month: 'short',
			timeZone: 'UTC',
			year: 'numeric',
		});
		days.set(language, formatter);
	}
	return formatter.format(new Date(`${isoDate}T00:00:00Z`));
}
```

- [ ] **Step 4: Run the test and watch it pass**

```bash
pnpm --filter @kurze-url/web test -- format
```

Expected: PASS. If the English month abbreviation differs from `Sept` on this Node build, adjust the expectation to what ICU actually produces — do not adjust the implementation to match a guess.

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "feat(web): add locale number and date formatting" <ids>
```

---

### Task 3: Window resolution

The presets and the search parameters are the part worth testing without rendering anything.

**Files:**

- Create: `apps/web/src/lib/stats-window.ts`
- Create: `apps/web/src/lib/stats-window.test.ts`

**Interfaces:**

- Consumes: nothing.
- Produces:
  - `RETENTION_DAYS = 90`
  - `type StatsWindow = { from: string; to: string }`
  - `type StatsSearch = { from?: string; to?: string }`
  - `parseStatsSearch(search: { from?: unknown; to?: unknown }): StatsSearch`
  - `presetWindow(days: number, today: Date): StatsWindow`
  - `retentionFloor(today: Date): string`
  - `matchingPreset(window: StatsWindow, today: Date): number | undefined`

Task 9 uses `presetWindow` and `matchingPreset`; Task 10 uses `parseStatsSearch`.

- [ ] **Step 1: Write the failing test**

Create `apps/web/src/lib/stats-window.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import {
	matchingPreset,
	parseStatsSearch,
	presetWindow,
	retentionFloor,
	RETENTION_DAYS,
} from './stats-window.ts';

const TODAY = new Date('2026-09-18T11:30:00Z');

describe(presetWindow, () => {
	it('counts today as one of the days', () => {
		expect(presetWindow(7, TODAY)).toStrictEqual({ from: '2026-09-12', to: '2026-09-18' });
	});

	it('resolves the thirty-day preset', () => {
		expect(presetWindow(30, TODAY)).toStrictEqual({ from: '2026-08-20', to: '2026-09-18' });
	});

	it('resolves the longest preset to exactly the retention window', () => {
		expect(presetWindow(RETENTION_DAYS, TODAY)).toStrictEqual({
			from: retentionFloor(TODAY),
			to: '2026-09-18',
		});
	});
});

describe(retentionFloor, () => {
	// The endpoint's floor is today minus 89, which together with today is 90
	// days. Off by one here would ask for a day the endpoint silently drops.
	it('is eighty-nine days before today', () => {
		expect(retentionFloor(TODAY)).toBe('2026-06-21');
	});
});

describe(parseStatsSearch, () => {
	it('keeps a well-formed pair', () => {
		expect(parseStatsSearch({ from: '2026-09-01', to: '2026-09-18' })).toStrictEqual({
			from: '2026-09-01',
			to: '2026-09-18',
		});
	});

	it('drops a malformed date rather than passing it to the API', () => {
		expect(parseStatsSearch({ from: '01.09.2026', to: '2026-09-18' })).toStrictEqual({
			to: '2026-09-18',
		});
	});

	it('drops a well-shaped impossible date', () => {
		expect(parseStatsSearch({ from: '2026-02-31' })).toStrictEqual({});
	});

	it('drops a non-string value', () => {
		expect(parseStatsSearch({ from: 7, to: null })).toStrictEqual({});
	});

	// Absent means "let the endpoint apply its own default". The page must not
	// compute that default itself, or two systems own one number.
	it('returns an empty object when nothing was supplied', () => {
		expect(parseStatsSearch({})).toStrictEqual({});
	});
});

describe(matchingPreset, () => {
	it('recognises a window a preset would have produced', () => {
		expect(matchingPreset({ from: '2026-09-12', to: '2026-09-18' }, TODAY)).toBe(7);
	});

	it('returns undefined for a hand-picked window', () => {
		expect(matchingPreset({ from: '2026-09-03', to: '2026-09-11' }, TODAY)).toBeUndefined();
	});
});
```

- [ ] **Step 2: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- stats-window
```

Expected: FAIL, "Failed to resolve import ./stats-window.ts".

- [ ] **Step 3: Write the implementation**

Create `apps/web/src/lib/stats-window.ts`:

```ts
/**
 * The endpoint serves `bucket_start >= today − 89` and deletes anything older,
 * both derived from one constant in `apps/api/internal/api`. Ninety is that
 * window counted inclusively: today plus the eighty-nine days before it.
 */
export const RETENTION_DAYS = 90;

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

const ISO_DAY = /^\d{4}-\d{2}-\d{2}$/u;

/**
 * @param date - Any instant.
 * @returns Its UTC calendar date as YYYY-MM-DD.
 */
function toIsoDay(date: Date): string {
	return date.toISOString().slice(0, 10);
}

/**
 * @param date - The day to move from.
 * @param days - How many days to subtract.
 * @returns The resulting UTC calendar date as YYYY-MM-DD.
 */
function minusDays(date: Date, days: number): string {
	const moved = new Date(date.getTime());
	moved.setUTCDate(moved.getUTCDate() - days);
	return toIsoDay(moved);
}

/**
 * @param today - The current instant; injected so tests do not depend on the clock.
 * @returns The oldest day the endpoint still serves.
 */
export function retentionFloor(today: Date): string {
	return minusDays(today, RETENTION_DAYS - 1);
}

/**
 * @param days - How many days the window should span, today included.
 * @param today - The current instant.
 * @returns The window a preset button stands for.
 */
export function presetWindow(days: number, today: Date): StatsWindow {
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
export function parseStatsSearch(search: { from?: unknown; to?: unknown }): StatsSearch {
	const parsed: StatsSearch = {};
	for (const key of ['from', 'to'] as const) {
		const value = search[key];
		if (typeof value !== 'string' || !ISO_DAY.test(value)) continue;
		// `new Date('2026-02-31')` yields 3 March rather than throwing, so the
		// round trip is what rejects a well-shaped impossible date.
		const date = new Date(`${value}T00:00:00Z`);
		if (!Number.isNaN(date.getTime()) && toIsoDay(date) === value) parsed[key] = value;
	}
	return parsed;
}

/**
 * @param window - The window the endpoint reported.
 * @param today - The current instant.
 * @returns The preset that would have produced this window, or undefined for a hand-picked one.
 */
export function matchingPreset(window: StatsWindow, today: Date): number | undefined {
	return [7, 30, RETENTION_DAYS].find((days) => {
		const preset = presetWindow(days, today);
		return preset.from === window.from && preset.to === window.to;
	});
}
```

- [ ] **Step 4: Run the test and watch it pass**

```bash
pnpm --filter @kurze-url/web test -- stats-window
```

Expected: PASS, 11 tests.

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "feat(web): resolve analytics time windows" <ids>
```

---

### Task 4: The server function

**Files:**

- Modify: `apps/web/src/server/links.ts` (append beside the other link operations)
- Modify: `apps/web/src/server/links.test.ts` (a new `describe` block)

**Interfaces:**

- Consumes: `StatsSearch` from `apps/web/src/lib/stats-window.ts` (Task 3); `requireSession`, `flushSessionCookies`, `authedApiClient` already used by every other function in `links.ts`.
- Produces:
  - `getLinkStatsFor(request: Request, linkId: string, window: StatsSearch): Promise<LinkStats>`
  - `getLinkStatsFn` — a `createServerFn({ method: 'GET' })` taking `{ linkId: string; window: StatsSearch }`
  - `linkStatsQueryOptions(linkId: string, window: StatsSearch)` — Task 10 passes this to the loader's `queryClient` and to `useSuspenseQuery`.

- [ ] **Step 1: Write the failing test**

Append to `apps/web/src/server/links.test.ts`, inside the existing top-level `describe('links', …)` block. Add `getLinkStatsFor` to the destructured `await import('./links')` list at the top of the file.

```ts
describe('getLinkStatsFor', () => {
	it('sends the window as query parameters and returns the document', async () => {
		withSession('stats-token');
		let seenUrl: URL | undefined;
		server.use(
			http.get('*/v1/links/:linkId/stats', ({ request }) => {
				seenUrl = new URL(request.url);
				return HttpResponse.json({
					analytics_enabled: true,
					breakdowns: {},
					from: '2026-09-01',
					link_id: 'link-1',
					series: [],
					to: '2026-09-18',
					totals: { clicks: 3, human_clicks: 2, human_unique_visitors: 1, unique_visitors: 2 },
				});
			}),
		);

		const stats = await getLinkStatsFor(new Request('https://web.test/'), 'link-1', {
			from: '2026-09-01',
			to: '2026-09-18',
		});

		expect(seenUrl?.searchParams.get('from')).toBe('2026-09-01');
		expect(seenUrl?.searchParams.get('to')).toBe('2026-09-18');
		expect(stats.totals.clicks).toBe(3);
	});

	// An absent bound must not travel as an empty string: the endpoint would
	// read that as a supplied-but-malformed value rather than as absent, and
	// the page would lose the endpoint's own thirty-day default.
	it('omits a bound that was not supplied', async () => {
		withSession('stats-token');
		let seenUrl: URL | undefined;
		server.use(
			http.get('*/v1/links/:linkId/stats', ({ request }) => {
				seenUrl = new URL(request.url);
				return HttpResponse.json({
					analytics_enabled: true,
					breakdowns: {},
					from: '2026-08-20',
					link_id: 'link-1',
					series: [],
					to: '2026-09-18',
					totals: { clicks: 0, human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 },
				});
			}),
		);

		await getLinkStatsFor(new Request('https://web.test/'), 'link-1', {});

		expect(seenUrl?.searchParams.has('from')).toBe(false);
		expect(seenUrl?.searchParams.has('to')).toBe(false);
	});

	it('flushes refreshed session cookies onto the real response', async () => {
		const appended: string[] = [];
		mocks.getResponse.mockReturnValue({
			headers: { append: (_name: string, value: string) => appended.push(value) },
		});
		withSession('stats-token');
		server.use(
			http.get('*/v1/links/:linkId/stats', () =>
				HttpResponse.json({
					analytics_enabled: true,
					breakdowns: {},
					from: '2026-09-01',
					link_id: 'link-1',
					series: [],
					to: '2026-09-18',
					totals: { clicks: 0, human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 },
				}),
			),
		);

		await getLinkStatsFor(new Request('https://web.test/'), 'link-1', {});

		expect(appended).toContain('sb-access-token=refreshed; Path=/; HttpOnly');
	});
});
```

- [ ] **Step 2: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- links
```

Expected: FAIL, `getLinkStatsFor is not a function`.

- [ ] **Step 3: Write the implementation**

Append to `apps/web/src/server/links.ts`. Add `getLinkStats` to the existing import from `@kurze-url/api-client`, and `LinkStats` to its type import.

```ts
/**
 * Same `...For`/`...Fn` split as every other operation in this file, for the
 * same reason: `createServerFn` reaches for `getRequest()` internally and
 * throws "No Start context found" under Vitest, so the testable half takes a
 * `Request` as a plain parameter.
 *
 * `window` carries only the bounds the caller actually supplied. An absent
 * bound is omitted from the query string rather than sent empty: the endpoint
 * applies its own thirty-day default for an absent `from`, and sending `''`
 * would be a supplied-but-malformed value instead. The page deliberately does
 * not know what that default is — see stats-window.ts.
 *
 * @param request - The incoming request, read for its session.
 * @param linkId - The link to report on.
 * @param window - The `from`/`to` bounds, either, both, or neither.
 * @returns The statistics document, whose own `from`/`to` are the window actually used.
 */
export const getLinkStatsFor = createServerOnlyFn(
	async (request: Request, linkId: string, window: StatsSearch): Promise<LinkStats> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await getLinkStats({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			query: window,
			throwOnError: true,
		});
		return data;
	},
);

/** `getRequest()` inline, not inside `getLinkStatsFor`, for the same reason as `listLinksFn`. */
export const getLinkStatsFn = createServerFn({ method: 'GET' })
	.validator((data: { readonly linkId: string; readonly window: StatsSearch }) => data)
	.handler(
		async ({
			data,
		}: {
			readonly data: { readonly linkId: string; readonly window: StatsSearch };
		}) => getLinkStatsFor(getRequest(), data.linkId, data.window),
	);

/**
 * The window is part of the key, so moving between two windows and back is
 * served from cache rather than refetched, and a stale window's document can
 * never be shown under a new window's heading.
 *
 * @param linkId - The link to report on.
 * @param window - The bounds, which may be empty.
 * @returns Query options for the loader and for useSuspenseQuery.
 */
export const linkStatsQueryOptions = (linkId: string, window: StatsSearch) =>
	queryOptions({
		queryFn: async () => getLinkStatsFn({ data: { linkId, window } }),
		queryKey: ['link-stats', linkId, window.from ?? '', window.to ?? ''] as const,
	});
```

- [ ] **Step 4: Run the test and watch it pass**

```bash
pnpm --filter @kurze-url/web test -- links
```

Expected: PASS, including the three new tests.

- [ ] **Step 5: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "feat(web): fetch link statistics" <ids>
```

---

### Task 5: Install the generated components

Three components and three runtime dependencies. Its own task because it changes the dependency graph, and a reviewer should be able to reject that independently of any code that uses it.

**Files:**

- Create (generated, never edited): `apps/web/src/components/ui/chart.tsx`, `calendar.tsx`, `popover.tsx`
- Modify: `apps/web/package.json`, `pnpm-lock.yaml`

**Interfaces:**

- Consumes: nothing.
- Produces: `ChartContainer`, `ChartTooltip`, `ChartTooltipContent`, `ChartLegend`, `ChartLegendContent` and the `ChartConfig` type from `ui/chart`; `Calendar` from `ui/calendar`; `Popover`, `PopoverTrigger`, `PopoverContent` from `ui/popover`.

- [ ] **Step 1: Run the generator**

```bash
cd apps/web && pnpm exec shadcn add chart calendar popover
```

The style comes from `components.json` (`base-sera`); do not pass a style or preset flag. `calendar` pulls `react-day-picker` and `date-fns`; `chart` pulls `recharts@3.8.0`.

- [ ] **Step 2: Check what the generator actually wrote**

```bash
git status --porcelain -uall
grep -c "" apps/web/src/components/ui/{chart,calendar,popover}.tsx
```

Expected: exactly the three new files plus `package.json` and `pnpm-lock.yaml`. If the generator also rewrote an existing `ui/*` file, keep that rewrite — those files are generator-owned — but say so in the report, because a changed `button.tsx` can move every button in the app.

- [ ] **Step 3: Read `calendar.tsx` before assuming it matches the others**

`calendar` is the one component in this set that does **not** build on Base UI. It renders react-day-picker plus this repository's own `Button`, and that is correct rather than an inconsistency to repair — there is no Base UI calendar to migrate it to. Do not "align" it with the other primitives, and do not file its react-day-picker dependency as an accident.

It is already styled for this preset: the generated file carries `[--cell-radius:0]`, which is the Sera style's square corners, and `range_start`/`range_middle`/`range_end` classes, which is the range mode Task 9 needs. Confirm both are present in what the generator actually wrote:

```bash
grep -c "cell-radius:0\|range_middle" apps/web/src/components/ui/calendar.tsx
```

Expected: at least 2. If either is missing, the registry has changed since this plan was written — report that rather than hand-editing the file, which the next `shadcn add` would overwrite anyway.

- [ ] **Step 4: Confirm the new files are exempt from lint and format**

```bash
pnpm lint && pnpm format:check
```

Expected: clean. `generated.config.ts` already globs `apps/web/src/components/ui/**`, so these three are covered without any change. If either tool reports on them, do **not** edit the files — fix the glob.

- [ ] **Step 5: Confirm the dependencies landed as runtime dependencies**

```bash
node -e "const p=require('./apps/web/package.json');console.log(['recharts','react-day-picker','date-fns'].map(d=>d+': '+(p.dependencies[d]??'MISSING')).join('\n'))"
```

Expected: three versions, none `MISSING`. All three are imported by shipped components, so `dependencies` is correct — unlike `shadcn` itself, which belongs in `devDependencies`.

- [ ] **Step 6: Confirm the build still passes**

```bash
pnpm typecheck && pnpm --filter @kurze-url/web build
```

Expected: both clean. Recharts is the largest dependency this app has taken on; if the build warns about chunk size, note the figure in the report rather than acting on it.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "build(web): add chart, calendar and popover" <ids>
```

---

### Task 6: The summary

Four totals, and the two binary breakdowns that are not top-ten lists and must not be drawn as though they were.

**Files:**

- Create: `apps/web/src/components/stat-summary.tsx`
- Create: `apps/web/src/components/stat-summary.test.tsx`
- Create: `apps/web/src/components/stat-summary.stories.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `formatCount` from `lib/format.ts` (Task 2); `StatCounts` and `StatBreakdown` from `@kurze-url/api-client`.
- Produces: `StatSummary`, taking `{ totals: StatCounts; botStatus: StatBreakdown; qrVsRegular: StatBreakdown; language: Language }`.

- [ ] **Step 1: Add the translation keys**

In `en.json`, a new top-level `stats` object:

```json
	"stats": {
		"heading": "Statistics",
		"clicks": "Clicks",
		"uniqueVisitors": "Visitors",
		"humanClicks": "Human clicks",
		"humanUniqueVisitors": "Human visitors",
		"visitorsNote": "Counted per day — someone returning on three days counts three times.",
		"botStatus": "Human or bot",
		"qrVsRegular": "QR or direct",
		"noValue": "No data yet"
	}
```

And in `de.json`, the same keys:

```json
	"stats": {
		"heading": "Statistik",
		"clicks": "Klicks",
		"uniqueVisitors": "Besucher",
		"humanClicks": "Menschliche Klicks",
		"humanUniqueVisitors": "Menschliche Besucher",
		"visitorsNote": "Pro Tag gezählt — wer an drei Tagen wiederkommt, zählt dreimal.",
		"botStatus": "Mensch oder Bot",
		"qrVsRegular": "QR oder direkt",
		"noValue": "Noch keine Daten"
	}
```

- [ ] **Step 2: Write the failing test**

Create `apps/web/src/components/stat-summary.test.tsx`. Every component test in this plan needs the i18n provider — `useTranslation` finds no instance without one. Define this helper at the top of the test file, matching the pattern `link-form.test.tsx` already uses, and call it instead of `render`:

```tsx
/**
 * @param ui - The element under test.
 * @param language - Which catalogue to load; the provider's language drives `t()`, while a component's own `language` prop drives number and date formatting.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}
```

Imports: `import { I18nextProvider } from 'react-i18next';` and `import { createI18n } from '../i18n';` (from a route test, `'../../i18n'`).

The tests:

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { StatSummary } from './stat-summary.tsx';

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
		expect(screen.getByText('human')).toBeInTheDocument();
		expect(screen.getByText('75%')).toBeInTheDocument();
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
		expect(screen.getByText('regular')).toBeInTheDocument();
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
```

- [ ] **Step 3: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- stat-summary
```

Expected: FAIL, "Failed to resolve import ./stat-summary.tsx".

- [ ] **Step 4: Write the component**

Create `apps/web/src/components/stat-summary.tsx`. Use `Card`, `CardHeader`, `CardTitle` and `CardContent` from `./ui/card`. Remember that `CardTitle` renders a `<div>` with no `render` prop, so a real heading goes inside it rather than replacing it. Percentages are computed against the sum of the split's own values, never against `totals.clicks` — a breakdown's values sum to that dimension's total, which is the same number only when nothing was truncated.

The four totals are a `<dl>` of four `<div>` pairs: `<dt>` the label, `<dd>` the formatted count. The visitor caveat is a `<p>` beneath the visitors figure, referenced from its `<dd>` with `aria-describedby`, so a screen reader reaches it in order rather than as loose text.

- [ ] **Step 5: Run the test and watch it pass**

```bash
pnpm --filter @kurze-url/web test -- stat-summary
```

Expected: PASS, 5 tests.

- [ ] **Step 6: Write the stories**

Create `apps/web/src/components/stat-summary.stories.tsx` with a default story and a `Dark` story. The dark story sets the theme per-story, which is the mechanism the design-system work verified on Storybook 10:

```tsx
export const Dark: Story = {
	globals: { theme: 'dark' },
};
```

- [ ] **Step 7: Run the Storybook tests**

```bash
pnpm --filter @kurze-url/web test:storybook
```

Expected: PASS, including the two new stories. These run in real Chromium with the a11y addon at error level, so a contrast or labelling mistake fails here.

- [ ] **Step 8: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "feat(web): add the statistics summary" <ids>
```

---

### Task 7: The series chart

Four lines, two colours, two channels. Colour says which metric; stroke style says which population. This is the task where the accessibility work actually happens — a chart of SVG paths carries no data to a screen reader, so the same rows ship as a hidden table.

**Files:**

- Create: `apps/web/src/components/stat-series-chart.tsx`
- Create: `apps/web/src/components/stat-series-chart.test.tsx`
- Create: `apps/web/src/components/stat-series-chart.stories.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `formatCount`, `formatDay` (Task 2); `ChartContainer`, `ChartTooltip`, `ChartTooltipContent`, `ChartLegend`, `ChartLegendContent`, `type ChartConfig` from `./ui/chart` (Task 5); `Checkbox` from `./ui/checkbox`; `StatDay` from `@kurze-url/api-client`.
- Produces: `StatSeriesChart`, taking `{ series: readonly StatDay[]; from: string; to: string; language: Language }`.

- [ ] **Step 1: Add the translation keys**

Into the `stats` object of `en.json`:

```json
		"seriesHeading": "Over time",
		"showBots": "Show bot share",
		"seriesTableCaption": "The same figures as a table",
		"columnDate": "Date",
		"chartLabel": "Clicks and visitors from {{from}} to {{to}}: {{clicks}} clicks, {{visitors}} visitors"
```

And into `de.json`:

```json
		"seriesHeading": "Zeitlicher Verlauf",
		"showBots": "Bot-Anteil zeigen",
		"seriesTableCaption": "Dieselben Zahlen als Tabelle",
		"columnDate": "Datum",
		"chartLabel": "Klicks und Besucher vom {{from}} bis {{to}}: {{clicks}} Klicks, {{visitors}} Besucher"
```

- [ ] **Step 2: Write the failing test**

Create `apps/web/src/components/stat-series-chart.test.tsx`. Every component test in this plan needs the i18n provider — `useTranslation` finds no instance without one. Define this helper at the top of the test file, matching the pattern `link-form.test.tsx` already uses, and call it instead of `render`:

```tsx
/**
 * @param ui - The element under test.
 * @param language - Which catalogue to load; the provider's language drives `t()`, while a component's own `language` prop drives number and date formatting.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}
```

Imports: `import { I18nextProvider } from 'react-i18next';` and `import { createI18n } from '../i18n';` (from a route test, `'../../i18n'`).

The tests:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import { StatSeriesChart } from './stat-series-chart.tsx';

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
		expect(screen.getByRole('table')).toHaveTextContent('17 Sept 2026');
	});

	it('names the window and the totals in the chart image label', () => {
		renderWithI18n(
			<StatSeriesChart from="2026-09-16" language="en" series={SERIES} to="2026-09-18" />,
		);
		expect(screen.getByRole('img')).toHaveAccessibleName(/14 clicks/u);
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

	it('renders an empty series without throwing', () => {
		renderWithI18n(<StatSeriesChart from="2026-09-16" language="en" series={[]} to="2026-09-18" />);
		expect(screen.getByRole('table')).toBeInTheDocument();
	});
});
```

Add `within` to the `@testing-library/react` import.

- [ ] **Step 3: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- stat-series-chart
```

Expected: FAIL, "Failed to resolve import ./stat-series-chart.tsx".

- [ ] **Step 4: Write the component**

Create `apps/web/src/components/stat-series-chart.tsx`. The chart configuration is where the two channels are declared:

```tsx
const config = {
	clicks: { color: 'var(--chart-1)', label: t('stats.clicks') },
	unique_visitors: { color: 'var(--chart-5)', label: t('stats.uniqueVisitors') },
	human_clicks: { color: 'var(--chart-1)', label: t('stats.humanClicks') },
	human_unique_visitors: { color: 'var(--chart-5)', label: t('stats.humanUniqueVisitors') },
} satisfies ChartConfig;
```

`--chart-1` and `--chart-5` are the widest pair the ramp offers and still only about 3:1 apart, which is why the two human lines repeat those colours and differ by `strokeDasharray="4 4"` instead of taking two more ramp steps. Do not "fix" the duplicated colours by spreading the four lines across `--chart-1` through `--chart-4`: adjacent steps measure 1.25:1 and the chart would become unreadable for exactly the readers this design protects.

Structure:

- A `Card` whose `CardTitle` holds a real `<h2>`.
- The `Checkbox` for the bot toggle, labelled from `stats.showBots`, with its own `id` and a real `<label>`. Base UI's `Checkbox` needs that `id`: its hidden input's label association keys off it, and removing it breaks the accessible name — this was measured during the design-system work and is recorded in `CLAUDE.md`.
- `<div role="img" aria-label={…} aria-describedby={tableId}>` wrapping the `ChartContainer`. The label is built from `stats.chartLabel` with the window and the summed totals interpolated.
- Inside: a Recharts `LineChart` over `series`, with `CartesianGrid`, an `XAxis` whose `tickFormatter` is `formatDay`, `ChartTooltip`/`ChartTooltipContent`, `ChartLegend`/`ChartLegendContent`, and two or four `Line` elements. The human lines carry `strokeDasharray="4 4"`.
- A `<table id={tableId} className="sr-only">` with a `<caption>` from `stats.seriesTableCaption`, a header row, and one row per day. Columns follow the toggle: two when off, four when on.

Use `useId()` for `tableId` and for the checkbox, never a hardcoded string — the design-system work migrated every form to `useId()` precisely because two instances on one page collide otherwise.

- [ ] **Step 5: Run the test and watch it pass**

```bash
pnpm --filter @kurze-url/web test -- stat-series-chart
```

Expected: PASS, 6 tests. Recharts measures its container and renders nothing at zero width under jsdom; that is expected and is exactly why every assertion above reads the table or the label rather than the SVG.

- [ ] **Step 6: Write the stories and run them**

Create `apps/web/src/components/stat-series-chart.stories.tsx` with a default story, a `Dark` story carrying `globals: { theme: 'dark' }`, and a `BotsShown` story whose `play` function clicks the toggle. Then:

```bash
pnpm --filter @kurze-url/web test:storybook
```

Expected: PASS. Chromium renders the chart at a real width here, so this is the only place the lines are actually drawn — and where the a11y addon checks their contrast.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "feat(web): chart the daily click series" <ids>
```

---

### Task 8: The breakdown card

**Files:**

- Create: `apps/web/src/components/stat-breakdown-card.tsx`
- Create: `apps/web/src/components/stat-breakdown-card.test.tsx`
- Create: `apps/web/src/components/stat-breakdown-card.stories.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `formatCount` (Task 2); `StatBreakdown` from `@kurze-url/api-client`.
- Produces: `StatBreakdownCard`, taking `{ title: string; breakdown: StatBreakdown; language: Language }`.

- [ ] **Step 1: Add the translation keys**

Into `stats` in `en.json`:

```json
		"browser": "Browser",
		"os": "Operating system",
		"device": "Device",
		"country": "Country",
		"referrer": "Referrer",
		"utmSource": "Campaign source",
		"otherValues_one": "{{count}} further value",
		"otherValues_other": "{{count}} further values",
		"breakdownEmpty": "Nothing recorded in this window"
```

And in `de.json`:

```json
		"browser": "Browser",
		"os": "Betriebssystem",
		"device": "Gerät",
		"country": "Land",
		"referrer": "Verweis",
		"utmSource": "Kampagnenquelle",
		"otherValues_one": "{{count}} weiterer Wert",
		"otherValues_other": "{{count}} weitere Werte",
		"breakdownEmpty": "In diesem Zeitraum nichts erfasst"
```

- [ ] **Step 2: Write the failing test**

Create `apps/web/src/components/stat-breakdown-card.test.tsx`. Every component test in this plan needs the i18n provider — `useTranslation` finds no instance without one. Define this helper at the top of the test file, matching the pattern `link-form.test.tsx` already uses, and call it instead of `render`:

```tsx
/**
 * @param ui - The element under test.
 * @param language - Which catalogue to load; the provider's language drives `t()`, while a component's own `language` prop drives number and date formatting.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}
```

Imports: `import { I18nextProvider } from 'react-i18next';` and `import { createI18n } from '../i18n';` (from a route test, `'../../i18n'`).

The tests:

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { StatBreakdownCard } from './stat-breakdown-card.tsx';

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
```

- [ ] **Step 3: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- stat-breakdown-card
```

Expected: FAIL, "Failed to resolve import ./stat-breakdown-card.tsx".

- [ ] **Step 4: Write the component**

Create `apps/web/src/components/stat-breakdown-card.tsx`. A `Card` with a real `<h2>` inside `CardTitle`, then a `<table>` of value, clicks and visitors. Each row carries a share bar: a `<div role="presentation">` whose width is the row's clicks as a percentage of the dimension's own total, which is the sum of `values` plus `other_clicks` — not `totals.clicks`, which is a different number whenever anything was truncated. The bar is filled with `var(--chart-1)`.

`other_values > 0` appends one row, using i18next's plural suffixes (`otherValues_one`/`otherValues_other`) with `count`, so both languages pluralise through the library rather than through a hand-written conditional.

`values: null` means the dimension recorded nothing; render the empty message instead of a table.

- [ ] **Step 5: Run the test and watch it pass**

```bash
pnpm --filter @kurze-url/web test -- stat-breakdown-card
```

Expected: PASS, 6 tests.

- [ ] **Step 6: Write the stories and run them**

A default story, a `Dark` story with `globals: { theme: 'dark' }`, a `WithOtherValues` story, and an `Empty` story. Then:

```bash
pnpm --filter @kurze-url/web test:storybook
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "feat(web): add the breakdown card" <ids>
```

---

### Task 9: The window picker

Three presets and a calendar range. The calendar is bounded so the picker cannot ask for a window the endpoint would silently clamp.

**Files:**

- Create: `apps/web/src/components/stat-range-picker.tsx`
- Create: `apps/web/src/components/stat-range-picker.test.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `presetWindow`, `matchingPreset`, `retentionFloor`, `RETENTION_DAYS`, `type StatsWindow` (Task 3); `formatDay` (Task 2); `Calendar` (Task 5); `Popover`, `PopoverTrigger`, `PopoverContent` (Task 5); `Button` from `./ui/button`.
- Produces: `StatRangePicker`, taking `{ window: StatsWindow; language: Language; today: Date; onChange: (window: StatsWindow) => void }`.

`window` is the one the **response** reported, not the one in the URL. `today` is injected rather than read from the clock so the component is testable; the route passes `new Date()`.

- [ ] **Step 1: Add the translation keys**

Into `stats` in `en.json`:

```json
		"rangeLabel": "Time window",
		"preset7": "7 days",
		"preset30": "30 days",
		"preset90": "90 days",
		"customRange": "Choose dates",
		"rangeRetentionNote": "Statistics are kept for 90 days."
```

And in `de.json`:

```json
		"rangeLabel": "Zeitraum",
		"preset7": "7 Tage",
		"preset30": "30 Tage",
		"preset90": "90 Tage",
		"customRange": "Zeitraum wählen",
		"rangeRetentionNote": "Statistiken werden 90 Tage lang aufbewahrt."
```

- [ ] **Step 2: Write the failing test**

Create `apps/web/src/components/stat-range-picker.test.tsx`. Every component test in this plan needs the i18n provider — `useTranslation` finds no instance without one. Define this helper at the top of the test file, matching the pattern `link-form.test.tsx` already uses, and call it instead of `render`:

```tsx
/**
 * @param ui - The element under test.
 * @param language - Which catalogue to load; the provider's language drives `t()`, while a component's own `language` prop drives number and date formatting.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}
```

Imports: `import { I18nextProvider } from 'react-i18next';` and `import { createI18n } from '../i18n';` (from a route test, `'../../i18n'`).

The tests:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { StatRangePicker } from './stat-range-picker.tsx';

const TODAY = new Date('2026-09-18T11:30:00Z');
const THIRTY_DAYS = { from: '2026-08-20', to: '2026-09-18' };

describe(StatRangePicker, () => {
	it('marks the preset that matches the current window', () => {
		renderWithI18n(
			<StatRangePicker language="en" onChange={vi.fn()} today={TODAY} window={THIRTY_DAYS} />,
		);
		expect(screen.getByRole('button', { name: '30 days' })).toHaveAttribute('aria-pressed', 'true');
		expect(screen.getByRole('button', { name: '7 days' })).toHaveAttribute('aria-pressed', 'false');
	});

	it('marks no preset for a hand-picked window', () => {
		renderWithI18n(
			<StatRangePicker
				language="en"
				onChange={vi.fn()}
				today={TODAY}
				window={{ from: '2026-09-03', to: '2026-09-11' }}
			/>,
		);
		for (const name of ['7 days', '30 days', '90 days']) {
			expect(screen.getByRole('button', { name })).toHaveAttribute('aria-pressed', 'false');
		}
	});

	it('reports the window a preset stands for', async () => {
		const onChange = vi.fn();
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
				onChange={vi.fn()}
				today={TODAY}
				window={{ from: '2026-06-21', to: '2026-09-18' }}
			/>,
		);
		expect(screen.getByText(/21 Jun 2026/u)).toBeInTheDocument();
	});

	it('states how long statistics are kept', () => {
		renderWithI18n(
			<StatRangePicker language="en" onChange={vi.fn()} today={TODAY} window={THIRTY_DAYS} />,
		);
		expect(screen.getByText('Statistics are kept for 90 days.')).toBeInTheDocument();
	});
});
```

- [ ] **Step 3: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- stat-range-picker
```

Expected: FAIL, "Failed to resolve import ./stat-range-picker.tsx".

- [ ] **Step 4: Write the component**

Create `apps/web/src/components/stat-range-picker.tsx`.

A `<div role="group" aria-label={t('stats.rangeLabel')}>` holding three preset `Button`s and a `Popover`. Each preset carries `aria-pressed={matchingPreset(window, today) === days}` — `aria-pressed` and not a `Toggle` component, because these are mutually exclusive shortcuts rather than independent switches, and nothing in the component set is a radio-styled button group.

The popover trigger's label is `stats.customRange`, and its content is `Calendar` in range mode:

```tsx
<Calendar
	disabled={{ after: today, before: new Date(`${retentionFloor(today)}T00:00:00Z`) }}
	mode="range"
	onSelect={(range) => {
		/* both ends chosen → onChange */
	}}
	selected={{ from: new Date(`${window.from}T00:00:00Z`), to: new Date(`${window.to}T00:00:00Z`) }}
/>
```

`disabled` is what keeps the picker from producing a window the endpoint would clamp; without it a user can select a date from last year, watch the page answer with 90 days, and have no idea why. `onChange` fires only once both ends are chosen — react-day-picker reports a half-finished range with `to` undefined, and sending that would make `from` and `to` disagree.

The current window renders as text beside the trigger, built from `formatDay(window.from)` and `formatDay(window.to)`, and `stats.rangeRetentionNote` sits beneath the group.

- [ ] **Step 5: Run the test and watch it pass**

```bash
pnpm --filter @kurze-url/web test -- stat-range-picker
```

Expected: PASS, 5 tests.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "feat(web): add the statistics range picker" <ids>
```

---

### Task 10: The route

Everything above, wired to the URL, plus the three empty states that keep the page from lying.

**Files:**

- Create: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.stats.tsx`
- Create: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.stats.test.tsx`
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx` (a link to the statistics page)
- Modify: `apps/web/src/routeTree.gen.ts` (regenerated; commit it, this task requires it)
- Modify: `apps/web/src/i18n/locales/en.json`, `apps/web/src/i18n/locales/de.json`

**Interfaces:**

- Consumes: `linkStatsQueryOptions` (Task 4); `parseStatsSearch` (Task 3); `StatSummary` (Task 6); `StatSeriesChart` (Task 7); `StatBreakdownCard` (Task 8); `StatRangePicker` (Task 9); `loadLink` and `getLinkFn` from the sibling detail route; `classifyApiError` from `lib/api-errors`; `requireTeamId` from `../_authed`.
- Produces: the route at `/teams/$teamSlug/links/$linkId/stats`.

- [ ] **Step 1: Add the translation keys**

Into `stats` in `en.json`:

```json
		"backToLink": "Back to the link",
		"viewStatistics": "Statistics",
		"disabledTitle": "Click counting is off for this link",
		"disabledBody": "Nothing is recorded while it is off, so this is not the same as a link nobody clicked.",
		"disabledAction": "Turn it on in the link settings",
		"noClicksTitle": "No clicks in this window",
		"noClicksBody": "Try a longer window."
```

And in `de.json`:

```json
		"backToLink": "Zurück zum Link",
		"viewStatistics": "Statistik",
		"disabledTitle": "Für diesen Link wird nicht gezählt",
		"disabledBody": "Solange die Zählung aus ist, wird nichts erfasst — das ist etwas anderes als ein Link, den niemand geklickt hat.",
		"disabledAction": "In den Link-Einstellungen einschalten",
		"noClicksTitle": "Keine Klicks in diesem Zeitraum",
		"noClicksBody": "Versuche einen längeren Zeitraum."
```

- [ ] **Step 2: Write the failing test**

Create `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.stats.test.tsx`. Test the exported pieces rather than the whole router — the sibling `teams.$teamSlug.links.$linkId.test.ts` shows the pattern, exporting the loader-facing functions and calling them directly.

```tsx
import { describe, expect, it } from 'vitest';

import { statsView } from './teams.$teamSlug.links.$linkId.stats.tsx';

const TOTALS_ZERO = { clicks: 0, human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 };
const EMPTY_BREAKDOWNS = {
	bot_status: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	browser: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	country: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	device: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	os: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	qr_vs_regular: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	referrer: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
	utm_source: { other_clicks: 0, other_unique_visitors: 0, other_values: 0, values: null },
};

describe(statsView, () => {
	// The single easiest thing to get wrong on this page. An empty document
	// from a link with counting off means "not counted", not "not clicked",
	// and a chart of zeroes there would be a false statement.
	it('reports counting as off rather than showing empty charts', () => {
		expect(
			statsView({
				analytics_enabled: false,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: TOTALS_ZERO,
			}),
		).toBe('disabled');
	});

	it('reports an empty window when counting is on and nothing was clicked', () => {
		expect(
			statsView({
				analytics_enabled: true,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: TOTALS_ZERO,
			}),
		).toBe('empty');
	});

	it('reports data whenever there is at least one click', () => {
		expect(
			statsView({
				analytics_enabled: true,
				breakdowns: EMPTY_BREAKDOWNS,
				from: '2026-08-20',
				link_id: 'l1',
				series: [],
				to: '2026-09-18',
				totals: { ...TOTALS_ZERO, clicks: 1 },
			}),
		).toBe('data');
	});
});
```

- [ ] **Step 3: Run the test and watch it fail**

```bash
pnpm --filter @kurze-url/web test -- links.\$linkId.stats
```

Expected: FAIL, module not found.

- [ ] **Step 4: Write the route**

Create the route file. The exported decision function comes first, because it is the part worth testing without a router:

```tsx
/**
 * The three states the endpoint distinguishes, and the page must too.
 * Collapsing them is how this page would come to lie: a link with counting
 * switched off returns the same zeroes as a link nobody clicked.
 *
 * @param stats - The statistics document.
 * @returns Which of the three views to render.
 */
export function statsView(stats: LinkStats): 'data' | 'disabled' | 'empty' {
	if (!stats.analytics_enabled) return 'disabled';
	return stats.totals.clicks > 0 ? 'data' : 'empty';
}
```

Then the route:

```tsx
export const Route = createFileRoute('/_authed/teams/$teamSlug/links/$linkId/stats')({
	// Declared before loaderDeps/loader, not for readability: loaderDeps's own
	// `search` parameter is typed from this function's return type, and
	// moving it later makes that inference fall back to {}. The sibling links
	// index carries the same note for the same reason.
	validateSearch: (search: { from?: string; to?: string } & SearchSchemaInput): StatsSearch =>
		parseStatsSearch(search),
	beforeLoad: ({ context, params }) => ({
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	loaderDeps: ({ search }) => ({ from: search.from, to: search.to }),
	loader: async ({ context, deps, params }) => {
		const [stats, link] = await Promise.all([
			context.queryClient.ensureQueryData(linkStatsQueryOptions(params.linkId, deps)),
			loadLink(getLinkFn, params.linkId),
		]);
		return { link, stats };
	},
	component: RouteComponent,
	errorComponent: StatsError,
});
```

Two calls in parallel, the way `routes/index.tsx` already pairs its own: the statistics document carries `link_id` and no slug, so without the second the heading could not name the link.

`RouteComponent` renders, in order: the heading with the link's short URL and a back link; `StatRangePicker` with `window={{ from: stats.from, to: stats.to }}` — the response's window, never the URL's — and an `onChange` that calls `navigate({ search: next })`; then the branch on `statsView(stats)`. `disabled` and `empty` render `Empty`/`EmptyHeader`/`EmptyTitle`/`EmptyDescription`/`EmptyContent`; `disabled` renders no chart at all, while `empty` keeps the picker above it because widening the window is the obvious next move. `data` renders `StatSummary`, `StatSeriesChart` and the six `StatBreakdownCard`s in a responsive grid.

`StatsError` follows the sibling routes' `LinksError`/`DomainsError` shape: `classifyApiError`, a message per class, and `reportUnexpected` for anything unclassified. A hand-edited URL with `from` later than `to` reaches here as a 422 — that is the correct outcome, and the reason `parseStatsSearch` deliberately does not enforce the ordering itself.

- [ ] **Step 5: Add the entry point on the detail page**

In `teams.$teamSlug.links.$linkId.tsx`, add a `Link` to the new route, labelled `stats.viewStatistics`, near the heading. Use `buttonVariants({ variant: 'outline' })` on the `Link`, the idiom the two existing call sites already use, each with the `react/forbid-component-props` disable and its reason.

- [ ] **Step 6: Run the tests and watch them pass**

```bash
pnpm --filter @kurze-url/web test
```

Expected: the whole suite green, including the three new route tests. `routeTree.gen.ts` regenerates during the dev server or build; run `pnpm --filter @kurze-url/web generate-routes` if the new route is not picked up.

- [ ] **Step 7: Verify it in a browser**

```bash
pnpm dev
```

Open a link's statistics page. Check all three states by toggling `analytics_enabled` on the link and by choosing a window with no clicks. Check that a 90-day preset and a hand-picked range both survive a reload, and that the displayed window matches what the endpoint echoed.

- [ ] **Step 8: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "feat(web): add the link statistics page" <ids>
```

---

### Task 11: axe over the composed page

The per-component Storybook runs check each part in isolation. This checks the page as a whole, which is where the last design-system wave found its only critical defect — a landmark problem that no single component could show.

**Files:**

- Create: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.stats.a11y.test.tsx`

**Interfaces:**

- Consumes: everything from Tasks 6 to 10.
- Produces: nothing.

- [ ] **Step 1: Write the test**

Model it on `apps/web/src/components/authed-shell.a11y.test.tsx`: render the composed page body with representative data — a populated series, breakdowns with and without an "other" row, both binary splits — and run `AxeBuilder` with **no** `.withTags()`, so the default ruleset applies. The design-system wave learned that `region` is a best-practice rule that only runs when the tags are left off, and it was the rule that caught the real defect.

```tsx
const results = await new AxeBuilder({ page }).analyze();
expect(results.violations).toStrictEqual([]);
```

- [ ] **Step 2: Run it and read what it says**

```bash
pnpm --filter @kurze-url/web test -- stats.a11y
```

If it is green on the first run, prove it can fail: temporarily remove the `aria-label` from the chart's `role="img"` wrapper, watch it go red, then restore. A test that has never failed has not been shown to test anything.

- [ ] **Step 3: Fix whatever it finds**

Likely candidates, each fixed in our own components and never in `ui/*`: a heading level that skips, a chart container with a role but no name, a share bar that kept an implicit role, or the page having no landmark of its own because `SidebarInset` already renders the `<main>`.

- [ ] **Step 4: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "test(web): run axe over the statistics page" <ids>
```

---

### Task 12: Documentation

**Files:**

- Modify: `CLAUDE.md`
- Modify: `docs/superpowers/specs/2026-09-13-design-system-and-shell-design.md`
- Modify: `docs/planning/03-frontend.md`

- [ ] **Step 1: Update the stack table in `CLAUDE.md`**

The UI row gains Recharts (through shadcn `chart`), react-day-picker and date-fns, naming what each is for.

- [ ] **Step 2: Add the chart-ramp constraint to `CLAUDE.md`**

Under "Non-obvious constraints", a new entry recording: the ramp is theme-aware and the two blocks hold different values running in opposite directions; the measured floor against `--card` in each theme; that the ramp shipped failing WCAG 1.4.11 in both themes because Theme=Indigo left the base preset's values in place, the same root cause as the `--ring` defect; that one hue cannot separate four series, so the series chart puts metric on colour and population on stroke style; and that `chart-contrast.test.ts` is what keeps this from regressing. Point at `docs/superpowers/plans/2026-09-18-analytics-contrast.md` for the numbers.

- [ ] **Step 3: Close the open question in the design-system spec**

In `2026-09-13-design-system-and-shell-design.md`, under "The chart ramp is monochromatic", append a short "Answered 2026-09-18" paragraph: the question was whether five shades separate well enough, the answer is that they do not and that the ramp was also failing the contrast floor outright. Link to this spec. Keep the original text — the same way `docs/planning/03-frontend.md` kept its Radix argument beside the reversal.

- [ ] **Step 4: Add the chart rule to `docs/planning/03-frontend.md`**

In the accessibility section: a chart ships with a text equivalent, because an SVG of paths carries no data to a screen reader. Name the hidden-table-plus-`aria-describedby` pattern this page established.

- [ ] **Step 5: Run the full gate**

```bash
pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter @kurze-url/web test && pnpm --filter @kurze-url/web test:storybook && pnpm --filter @kurze-url/web build
```

Expected: all green.

- [ ] **Step 6: Commit**

```bash
pnpm format
but commit -b feat/link-analytics-page -m "docs: record the chart ramp constraint" <ids>
```
