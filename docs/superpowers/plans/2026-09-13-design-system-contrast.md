# Palette contrast baseline

Date: 2026-09-13

This records the contrast ratios of the `indigo` Theme's four load-bearing colour pairs, for both light and dark mode, as a baseline that later changes can be checked against. The Sera style draws input fields with an underline rather than a box, which makes the focus indicator carry more of the affordance than it normally would — the `--ring` pair is the one this task exists for.

## How the ratios were derived

The token values were read directly from the literal `oklch(...)` declarations under `[data-theme='indigo']` (light) and `[data-theme='indigo'].dark` (dark) in `apps/web/src/styles/app.css`, then converted by hand — OKLCH → OKLab → linear sRGB (Björn Ottosson's published matrices) → WCAG relative luminance → contrast ratio — in a throwaway Node script that was deleted after use, not read out of a running browser. The same script was reused, unmodified, to check the replacement `--ring` values described below.

The conversion was validated before trusting it: pure black (`oklch(0 0 0)`) against pure white (`oklch(1 0 0)`) returned exactly **21.000:1**, the known maximum WCAG ratio, and a mid-grey (`oklch(0.6 0 0)`) against white returned a plausible **3.947:1**, confirming the luminance curve behaves as expected rather than, say, treating OKLCH lightness as linear luminance directly.

## Token values (current)

| Token                  | Light                        | Dark                         |
| ---------------------- | ---------------------------- | ---------------------------- |
| `--background`         | `oklch(1 0 0)`               | `oklch(0.145 0 0)`           |
| `--foreground`         | `oklch(0.145 0 0)`           | `oklch(0.985 0 0)`           |
| `--primary`            | `oklch(0.457 0.24 277.023)`  | `oklch(0.398 0.195 277.366)` |
| `--primary-foreground` | `oklch(0.962 0.018 272.314)` | `oklch(0.962 0.018 272.314)` |
| `--muted-foreground`   | `oklch(0.556 0 0)`           | `oklch(0.708 0 0)`           |
| `--ring`               | `oklch(0.511 0.262 276.966)` | `oklch(0.585 0.233 277.117)` |

`--ring` is no longer the preset's neutral grey in either mode — see "The original failure and the fix" below for what it shipped as and why it changed.

## Measurements (current)

| Mode  | Pair                                   | Threshold | Ratio    | Result |
| ----- | -------------------------------------- | --------- | -------- | ------ |
| Light | `--foreground` on `--background`       | 4.5:1     | 19.793:1 | Pass   |
| Light | `--primary-foreground` on `--primary`  | 4.5:1     | 7.216:1  | Pass   |
| Light | `--muted-foreground` on `--background` | 4.5:1     | 4.732:1  | Pass   |
| Light | `--ring` on `--background`             | 3:1       | 6.441:1  | Pass   |
| Dark  | `--foreground` on `--background`       | 4.5:1     | 18.958:1 | Pass   |
| Dark  | `--primary-foreground` on `--primary`  | 4.5:1     | 9.022:1  | Pass   |
| Dark  | `--muted-foreground` on `--background` | 4.5:1     | 7.633:1  | Pass   |
| Dark  | `--ring` on `--background`             | 3:1       | 4.324:1  | Pass   |

All eight measurements now pass.

## The original failure and the fix

The first measurement of this palette found **light-mode `--ring` on `--background` failing WCAG 1.4.11** (non-text contrast, 3:1 threshold): the preset's neutral ring, `oklch(0.708 0 0)`, measured **2.593:1** against a white background. Dark mode's neutral ring, `oklch(0.556 0 0)`, passed at 4.183:1.

The root cause: Theme=Indigo recoloured `--primary` in both modes but left `--ring` as the base preset's neutral grey in both modes too, so the one token this style leans on most heavily — the focus ring, given underline-only inputs — was never actually part of the recolour.

This was reported rather than silently patched, as the task required. It was then adjudicated as a defect to fix, not a finding to file and move past: `CLAUDE.md`'s golden rule 8 makes WCAG 2.1 AA a requirement, and the Sera style makes the ring the primary affordance on every form in the app, which is exactly why this measurement was asked for in the first place.

The fix draws both rings from the indigo ramp already in the file, rather than from `--primary` directly:

- **Light `--ring`** is now `oklch(0.511 0.262 276.966)` — `--chart-3`, and the same value this mode already uses for `--sidebar-primary`. It measures **6.441:1**.
- **Dark `--ring`** is now `oklch(0.585 0.233 277.117)` — `--chart-2`, and the same value this mode already uses for `--sidebar-primary`. It measures **4.324:1**. Dark mode's neutral ring already passed, but it was changed too so both modes draw the ring from the same recoloured indigo family as `--primary`, consistent with the root cause above.

Copying `--primary` straight into `--ring` was considered and rejected: dark mode's `--primary` is `--chart-5` (`oklch(0.398 0.195 277.366)`), a dark indigo that measures only **1.962:1** against the near-black dark background — copying it would have fixed light mode while silently breaking dark mode's ring, which passed before this change. Each candidate step of the ramp was measured against its own mode's background before being chosen; light and dark deliberately use different steps of the same hue family rather than one shared value.

The change lives in `apps/web/src/styles/app.css`, with a comment at each `--ring` declaration recording the measured baseline and the reason it is no longer the preset's neutral.
