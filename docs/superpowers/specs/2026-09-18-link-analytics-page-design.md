# Link Analytics Page — Design

**Status:** approved 2026-09-18 **Amends:** `CLAUDE.md` (a new non-obvious constraint: the chart ramp is theme-aware and why; the stack table's UI row gains Recharts, react-day-picker and date-fns), `docs/superpowers/specs/2026-09-13-design-system-and-shell-design.md` (its open question "The chart ramp is monochromatic" is answered here, and the answer is larger than the question — the ramp was also failing WCAG 1.4.11 in both themes).

The fifteenth implementation spec, and the first one that draws a chart.

`GET /v1/links/{link_id}/stats` shipped on 2026-09-11 and has never been read by a browser. Its own spec said so explicitly: the frontend was left out because no charting library was installed and the design pass had not happened yet. Both conditions are now gone. The design system shipped on 2026-09-14 on Base UI with the `base-sera` style, and shadcn's `chart` component is available for that style.

This spec builds the page that reads the endpoint, and nothing else.

## Goal

A team member can open a link and see what it actually did: how many clicks and visitors over a window they choose, day by day, and which browsers, systems, devices, countries, referrers and campaigns those clicks came from.

## Scope

### In scope

- One new route, `/teams/$teamSlug/links/$linkId/stats`, with the window in its search parameters.
- One new server function, `getLinkStatsFn`, following the `...For`/`...Fn` split the rest of `server/links.ts` uses.
- The `chart`, `calendar` and `popover` components from the generator, and the three runtime dependencies they bring.
- A theme-aware chart ramp, measured and recorded.
- Number and date formatting bound to the active language — the first `Intl` use in this repository.
- Storybook stories, unit tests, and an axe run over the composed page.

### Out of scope, and where each lands

- **Team-level or account-level aggregates.** No endpoint answers them, and adding one is an API spec, not a page.
- **CSV or any export.** The stats spec already refused it for want of a requester, and nothing has changed.
- **Hourly granularity.** `bucket_start` is a `date`. Doc 05 decided daily for the MVP, and the endpoint has no hourly mode to render.
- **Cross-dimension filtering** — clicks from Chrome _in Germany_, or any breakdown split by bot status. The rollup holds one row per dimension per day and never a row for a pair, so no page can show it and no API change recovers it. `CLAUDE.md` already records this; the page must not imply otherwise by putting the bot toggle anywhere near the breakdowns.
- **A link list column showing click counts.** That needs a different query — the list endpoint returns no counts — and would put a per-link aggregate on a paginated page.
- **TanStack Table filtering on the link list.** Still outstanding, still its own change.

## Global constraints

Inherited and not re-litigated here:

- i18n from the first component. No hardcoded user-facing string, including chart labels, axis ticks, empty states and the accessible names of every control.
- Accessibility is a requirement, checked in CI at two levels. WCAG 2.1 AA.
- A non-member gets 404, never 403. The route inherits this through the existing `_authed` resolution; it adds no authorization logic of its own.
- Generated components under `src/components/ui/` are never hand-edited. Customisation lives in a wrapper, in tokens, or in the preset.
- Conventional Commits, subject capped at 50 characters including type and scope; `pnpm format` before every commit; the Lefthook hooks are not bypassed.
- `apps/web/src/routeTree.gen.ts` is committed only where the change requires it. This spec adds a route, so it does.

## The chart ramp, and what measuring it found

The design-system spec left this as an open question: five lightnesses of one indigo hue separate less well than five hues, and the analytics endpoint serves eight categorical dimensions. It proposed direct labelling first and a separate categorical palette second.

Measuring the tokens answered that question and found a defect underneath it.

**The ramp is not theme-aware.** `--chart-1` through `--chart-5` appear in both the light and the dark block of `src/styles/app.css` with identical values. Charts render inside a `Card`, so the background is `--card`: `oklch(1 0 0)` light, `oklch(0.205 0 0)` dark. Measured against it, with WCAG 1.4.11's 3:1 floor for graphical objects:

| Token       | light     | dark      |
| ----------- | --------- | --------- |
| `--chart-1` | **2.009** | 8.916     |
| `--chart-2` | 4.577     | 3.914     |
| `--chart-3` | 6.441     | **2.781** |
| `--chart-4` | 8.067     | **2.221** |
| `--chart-5` | 10.086    | **1.776** |

One token, `--chart-2`, clears the floor in both themes. A fixed ramp therefore cannot carry even two series across both themes, never mind five. This is the same root cause as the `--ring` defect the design-system work fixed: Theme=Indigo recoloured some tokens and left others at the base preset's values in both blocks.

### The fix, in two parts

**The ramp becomes theme-aware**, like `--ring` already is. Light-mode steps stay dark enough to clear white; dark-mode steps stay light enough to clear the dark card. Every value below is inside the sRGB gamut — chroma is set to 92% of the maximum the hue admits at that lightness, so nothing is silently clamped and the painted colour is the specified one.

Light, under `[data-theme='indigo']`:

```css
--chart-1: oklch(0.648 0.175 277); /* 3.406 against --card */
--chart-2: oklch(0.585 0.211 277); /* 4.491 */
--chart-3: oklch(0.511 0.256 277); /* 6.408 */
--chart-4: oklch(0.457 0.26 277); /* 8.223 */
--chart-5: oklch(0.398 0.227 277); /* 10.369 */
```

Dark, under `[data-theme='indigo'].dark`:

```css
--chart-1: oklch(0.585 0.211 277); /* 3.989 against --card */
--chart-2: oklch(0.66 0.168 277); /* 5.532 */
--chart-3: oklch(0.732 0.129 277); /* 7.368 */
--chart-4: oklch(0.806 0.09 277); /* 9.662 */
--chart-5: oklch(0.88 0.054 277); /* 12.404 */
```

**And colour never carries meaning alone.** Even after the fix the ramp is one hue, and one hue does not separate series. The widest available pair, `--chart-1` against `--chart-5`, measures 3.045 in light and 3.110 in dark; the pair two steps apart measures 1.88. Four distinguishable shades do not exist in this palette and no recolouring within one hue creates them.

So the series chart uses **two visual channels, one variable each**:

- **Colour says which metric.** Clicks are `--chart-1`, unique visitors are `--chart-5` — the widest pair.
- **Stroke says which population.** Solid is everyone, dashed is humans only.

Four lines, two colours, and both variables legible to a reader who cannot distinguish the two indigos at all. This is a better answer than the categorical palette the design-system spec imagined, and it needs no new tokens.

The remaining steps are not orphaned: `--chart-1` fills the share bars in the breakdown cards, where values are compared within one card and never across.

## The route and its window

`apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.stats.tsx`.

The window lives in the search parameters, not in component state, so a window is a URL somebody can bookmark or paste into a message. Both are optional and both are `YYYY-MM-DD`:

```
/teams/sv-gruenwald/links/<id>/stats?from=2026-08-20&to=2026-09-18
```

Absent, the page sends neither and the endpoint applies its own defaults — the last 30 days. The page does not compute that default itself. Duplicating a default is how two systems drift, and the endpoint already echoes the window it actually used in `from` and `to`, which is what the picker displays.

`validateSearch` accepts only the two keys and only that shape, rejecting anything else to the route's default. It does **not** enforce `from <= to`: the endpoint answers 422 for that pair, the picker cannot produce it, and re-implementing the rule here would make the frontend a second, quieter authority on it. A hand-edited URL that violates it reaches the error component, which is the correct outcome.

The loader fetches two things in parallel, the way `routes/index.tsx` already does: the statistics, and the link itself. The stats document carries `link_id` and no slug, so without the second call the heading could not name the link it describes.

The entry point is a link on the link detail page. The sidebar gains nothing: statistics belong to a link, not to a team, and a sidebar entry would have no link to point at.

## The page

**Heading.** The link's short URL, and a back link to the detail page.

**Window picker.** Three presets — 7, 30 and 90 days — and a calendar popover for any other range. 90 is not an arbitrary largest preset; it is the retention window, beyond which the endpoint clamps and there is nothing to show. Choosing a preset or a range rewrites the search parameters, which re-runs the loader. The calendar is disabled beyond today and before the retention floor, so the picker cannot ask for a window the endpoint would silently clamp.

**Summary.** Four figures from `totals`: clicks, unique visitors, human clicks, human unique visitors. Beneath them the two binary breakdowns as labelled pairs with a percentage — `bot_status` and `qr_vs_regular`. These two are not top-ten lists and must not be rendered as though they were.

`unique_visitors` carries a caveat the endpoint states in its own OpenAPI description and the page must repeat: over several days it is the sum of daily uniques, so a person returning on three days counts three times. It appears as a short note next to the figure, not buried in a tooltip, because the field name says the opposite of what the number means.

**Series.** Two solid lines, plus two dashed ones when "show bot share" is on. The series is gap-filled by the endpoint — every day of the window has an entry — so nothing is interpolated and a day with no clicks is a real zero.

**Breakdowns.** Six cards in a responsive grid: browser, OS, device, country, referrer, `utm_source`. Each card lists its values with clicks, unique visitors and a share bar. When `other_values > 0` the card ends with a row naming how many further values exist and what they account for. That row is not optional: without it a list capped at ten silently misstates its own dimension's total, which is exactly why the endpoint returns the three `other_*` fields.

## Three empty states, not one

The endpoint distinguishes them and so must the page. Collapsing them is the likeliest way to make this page lie.

1. **`analytics_enabled === false`** — counting is switched off for this link. The redirect path records nothing, so an empty document means _not counted_, not _not clicked_. The page renders an `Empty` with a link to the edit form and **no charts at all**. A chart of zeroes here would be a false statement.
2. **Enabled, `totals.clicks === 0`** — no clicks in this window. The picker stays, because widening the window is the obvious next move.
3. **One breakdown with no values** — only that card is empty. The others still render.

## Formatting is new here

Nothing in this repository formats a number or a date for a locale today; a grep for `Intl` finds nothing. A German reader currently sees `1234` and `2026-09-15`. A page whose entire content is numbers and dates is where that stops being acceptable.

`src/lib/format.ts` provides `formatCount` and `formatDay`, both taking the active language. They must produce the same output on the server and in the browser, or React reports a hydration mismatch: the language already travels through the existing preferences cookie and is available in both places, so the formatters take it as an argument rather than reading a global.

## Accessibility

Charts are where accessible pages usually stop being accessible, so this is specified rather than assumed.

- **The series is available as text.** A visually hidden table holds the same rows the chart draws, and the chart's container references it with `aria-describedby`. This is the only way the data reaches a screen reader; an SVG of paths is not data.
- **The chart's SVG takes `role="img"`** and an `aria-label` naming the window and the totals, so a reader who does not open the table still learns what the picture says.
- **The share bars are `role="presentation"`.** The number beside each bar is the information; the bar repeats it visually.
- **Colour is never the only carrier** — see the ramp section. The legend names every line, and the dashed lines are described as such in their accessible names, not only drawn that way.
- **The window picker is operable from the keyboard**, including the calendar, and its current selection is announced. A preset button that is active carries `aria-pressed`.
- **axe runs over the composed page**, the way `authed-shell.a11y.test.tsx` runs over the shell, with the default ruleset rather than a narrowed one.

## Components and files

Generated, by `shadcn add`, never hand-edited:

- `src/components/ui/chart.tsx` — brings `recharts@3.8.0`
- `src/components/ui/calendar.tsx` — brings `react-day-picker` and `date-fns`
- `src/components/ui/popover.tsx`

Written here:

| File | Responsibility |
| --- | --- |
| `src/server/links.ts` | `getLinkStatsFor` and `getLinkStatsFn`, added to the existing file beside the other link operations |
| `src/routes/_authed/teams.$teamSlug.links.$linkId.stats.tsx` | Route, search validation, loader, layout, error component |
| `src/components/stat-range-picker.tsx` | Presets plus the calendar popover; writes search parameters |
| `src/components/stat-summary.tsx` | The four totals and the two binary splits |
| `src/components/stat-series-chart.tsx` | The line chart, the bot toggle, and the hidden data table |
| `src/components/stat-breakdown-card.tsx` | One dimension's list, including the "other values" row |
| `src/lib/format.ts` | `formatCount`, `formatDay` |
| `src/lib/stats-window.ts` | Preset to `from`/`to`, search-parameter parsing, the retention floor |

`stats-window.ts` is separate from the picker on purpose: the resolution rules are the part worth testing without rendering anything, and the picker is the part worth testing by clicking.

## Hazards for whoever implements this

- **`calendar` does not use Base UI.** It renders react-day-picker plus this repository's own `Button`. Nothing about it conflicts with the primitive layer, and nothing about it should be "migrated" to match the others.
- **The endpoint's clamping is silent.** A request for a year returns 90 days with no warning beyond the echoed `from` and `to`. The picker must display the window from the _response_, never the one from the URL, or a clamped request shows a range the data does not cover.
- **`analytics_enabled` is not the same as "no data".** See the empty states. This is the single easiest thing to get wrong on this page.
- **Recharts renders nothing during SSR with zero width.** The chart must tolerate being measured at zero and must not throw; the hidden data table is what SSR delivers regardless.
- **The two binary breakdowns can be empty too.** A link with no QR clicks has one value in `qr_vs_regular`, not two, and the summary must not assume a pair exists.
- **A dimension's values are attacker-supplied text.** `referrer` and `utm_source` come from the request. They are truncated to 128 bytes by the API but are otherwise arbitrary; render them as text and never as a URL the page will fetch or link to.

## Testing

- **Unit** — `stats-window.ts` (preset resolution, search parsing, the retention floor, rejection of malformed input) and `format.ts` (both languages, and identical output for the same inputs).
- **RTL** — the three empty states; the bot toggle adding exactly two lines; the picker writing search parameters; the "other values" row appearing only when `other_values > 0`.
- **Storybook** — the series chart and a breakdown card, each light and dark, using the per-story `globals: { theme: 'dark' }` the design-system work established.
- **axe** — over the composed page, default ruleset, zero violations.
- **Contrast** — every ramp value measured against its own `--card` and written to `docs/superpowers/plans/2026-09-18-analytics-contrast.md`, in the shape `2026-09-13-design-system-contrast.md` established, including the failing pre-fix numbers as a permanent record of what was wrong.

## Documentation this changes

- `CLAUDE.md` — the stack table's UI row gains Recharts, react-day-picker and date-fns; a new non-obvious constraint records that the chart ramp is theme-aware, why a fixed ramp cannot work, and that colour alone never distinguishes a series here.
- `docs/superpowers/specs/2026-09-13-design-system-and-shell-design.md` — its open question is answered by this document; the answer is recorded there as a pointer rather than duplicated.
- `docs/planning/03-frontend.md` — the accessibility section gains the rule that a chart ships with a text equivalent.
