# Chart ramp contrast baseline

Date: 2026-09-18

This records the contrast of the `indigo` theme's five `--chart-*` tokens against `--card`, the background the analytics chart actually draws on (it renders inside a `Card`, not directly on `--background`). It is the same kind of baseline `2026-09-13-design-system-contrast.md` recorded for `--ring` and `--sidebar-ring` — measured before the fix, so a future reader can see what the trap was, not only that it is now fixed.

## How the ratios were derived

`apps/web/src/styles/chart-contrast.test.ts` reads the `oklch(...)` literals straight out of `app.css` (rather than a fixture) and runs the same OKLCH → linear sRGB → WCAG relative luminance → contrast ratio pipeline as the earlier `--ring` baseline: Björn Ottosson's published OKLab matrices, each channel clamped into `0..1` the way a browser paints an out-of-gamut colour, then the standard `(L1 + 0.05) / (L2 + 0.05)` ratio. Because the test reads the stylesheet itself, it fails if someone edits a token without touching the test — a fixture copy could not do that.

## Token values

| Token | Light — before | Light — after | Dark — before | Dark — after |
| --- | --- | --- | --- | --- |
| `--chart-1` | `oklch(0.785 0.115 274.713)` | `oklch(0.648 0.175 277)` | `oklch(0.785 0.115 274.713)` | `oklch(0.585 0.211 277)` |
| `--chart-2` | `oklch(0.585 0.233 277.117)` | `oklch(0.585 0.211 277)` | `oklch(0.585 0.233 277.117)` | `oklch(0.66 0.168 277)` |
| `--chart-3` | `oklch(0.511 0.262 276.966)` | `oklch(0.511 0.256 277)` | `oklch(0.511 0.262 276.966)` | `oklch(0.732 0.129 277)` |
| `--chart-4` | `oklch(0.457 0.24 277.023)` | `oklch(0.457 0.26 277)` | `oklch(0.457 0.24 277.023)` | `oklch(0.806 0.09 277)` |
| `--chart-5` | `oklch(0.398 0.195 277.366)` | `oklch(0.398 0.227 277)` | `oklch(0.398 0.195 277.366)` | `oklch(0.88 0.054 277)` |

Before the fix, both blocks carried the same five values — the light ramp, copied verbatim into the dark block. `--card` is `oklch(1 0 0)` (white) in light mode and `oklch(0.205 0 0)` in dark mode, so a ramp tuned for a white background is not automatically legible on a near-black one.

## Measurements — before (the failing state)

| Mode  | Token       | Threshold | Ratio  | Result   |
| ----- | ----------- | --------- | ------ | -------- |
| Light | `--chart-1` | 3:1       | 2.009  | **Fail** |
| Light | `--chart-2` | 3:1       | 4.577  | Pass     |
| Light | `--chart-3` | 3:1       | 6.441  | Pass     |
| Light | `--chart-4` | 3:1       | 8.067  | Pass     |
| Light | `--chart-5` | 3:1       | 10.086 | Pass     |
| Dark  | `--chart-1` | 3:1       | 8.916  | Pass     |
| Dark  | `--chart-2` | 3:1       | 3.914  | Pass     |
| Dark  | `--chart-3` | 3:1       | 2.781  | **Fail** |
| Dark  | `--chart-4` | 3:1       | 2.221  | **Fail** |
| Dark  | `--chart-5` | 3:1       | 1.776  | **Fail** |

Light `--chart-1` and three of dark's five steps sat below the 3:1 floor WCAG 1.4.11 asks for between a graphical object (a chart line) and its background. The root cause: the dark block had never been given its own ramp — it held the light block's values unchanged, and a ramp chosen to read against white does not automatically read against near-black.

## Measurements — after (the fix)

| Mode  | Token       | Threshold | Ratio  | Result |
| ----- | ----------- | --------- | ------ | ------ |
| Light | `--chart-1` | 3:1       | 3.406  | Pass   |
| Light | `--chart-2` | 3:1       | 4.491  | Pass   |
| Light | `--chart-3` | 3:1       | 6.408  | Pass   |
| Light | `--chart-4` | 3:1       | 8.223  | Pass   |
| Light | `--chart-5` | 3:1       | 10.369 | Pass   |
| Dark  | `--chart-1` | 3:1       | 3.989  | Pass   |
| Dark  | `--chart-2` | 3:1       | 5.532  | Pass   |
| Dark  | `--chart-3` | 3:1       | 7.368  | Pass   |
| Dark  | `--chart-4` | 3:1       | 9.662  | Pass   |
| Dark  | `--chart-5` | 3:1       | 12.404 | Pass   |

All ten now pass. The light ramp runs dark-to-light-limited — nothing above `L 0.648` can clear white at 3:1 — so `--chart-1` sits at the bottom of what the light background allows. The dark ramp runs the other way: nothing below `L 0.585` clears the dark card, so its steps climb from `--chart-1` up to a near-white `--chart-5`. Each value stays inside the sRGB gamut at 92% of the maximum chroma this hue admits at its lightness, so the painted colour is the one specified rather than a silently clamped neighbour.

## The two default series

The chart draws two series by default — clicks (`--chart-1`) and unique visitors (`--chart-5`) — the two ends of the ramp, chosen because they are the widest pair it offers:

| Mode  | Pair                      | Ratio |
| ----- | ------------------------- | ----- |
| Light | `--chart-1` / `--chart-5` | 3.045 |
| Dark  | `--chart-1` / `--chart-5` | 3.110 |

Both clear 3:1, but only just. `chart-contrast.test.ts` pins this pairing deliberately: if the ramp is ever renumbered, the chart's two default series have to be renumbered with it, or this margin disappears.

## Why colour alone is not enough

A single hue ramp cannot separate four or five series from each other the way distinct hues could — every step here is the same indigo at a different lightness, so two steps close together in the ramp (say `--chart-2` and `--chart-3`) are far short of 3:1 apart from _each other_, even though each clears 3:1 against the card individually. That is a deliberate trade-off, not an oversight: a multi-hue ramp would fix series-to-series separation but reintroduces the risk this task just fixed, of some hue's lightness failing against one theme's card and not the other. This is exactly why the chart distinguishes its series with two independent channels rather than relying on the ramp to carry the distinction alone: colour says which **metric** — clicks on `--chart-1`, unique visitors on `--chart-5` — and stroke style says which **population** — solid for everyone, dashed for humans only. Neither channel depends on how close two lightness steps happen to sit.
