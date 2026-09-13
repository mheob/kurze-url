# Palette contrast baseline

Date: 2026-09-13

This records the contrast ratios of the `indigo` Theme's four load-bearing colour pairs, for both light and dark mode, as a baseline that later changes can be checked against. The Sera style draws input fields with an underline rather than a box, which makes the focus indicator carry more of the affordance than it normally would — the `--ring` pair is the one this task exists for.

## How the ratios were derived

The token values below were read directly from the literal `oklch(...)` declarations under `[data-theme='indigo']` (light) and `[data-theme='indigo'].dark` (dark) in `apps/web/src/styles/app.css`, then converted by hand — OKLCH → OKLab → linear sRGB (Björn Ottosson's published matrices) → WCAG relative luminance → contrast ratio — in a throwaway Node script that was deleted after use, not read out of a running browser.

The conversion was validated before trusting it: pure black (`oklch(0 0 0)`) against pure white (`oklch(1 0 0)`) returned exactly **21.000:1**, the known maximum WCAG ratio, and a mid-grey (`oklch(0.6 0 0)`) against white returned a plausible **3.947:1**, confirming the luminance curve behaves as expected rather than, say, treating OKLCH lightness as linear luminance directly.

## Token values read

| Token                  | Light                        | Dark                         |
| ---------------------- | ---------------------------- | ---------------------------- |
| `--background`         | `oklch(1 0 0)`               | `oklch(0.145 0 0)`           |
| `--foreground`         | `oklch(0.145 0 0)`           | `oklch(0.985 0 0)`           |
| `--primary`            | `oklch(0.457 0.24 277.023)`  | `oklch(0.398 0.195 277.366)` |
| `--primary-foreground` | `oklch(0.962 0.018 272.314)` | `oklch(0.962 0.018 272.314)` |
| `--muted-foreground`   | `oklch(0.556 0 0)`           | `oklch(0.708 0 0)`           |
| `--ring`               | `oklch(0.708 0 0)`           | `oklch(0.556 0 0)`           |

## Measurements

| Mode  | Pair                                   | Threshold | Ratio    | Result   |
| ----- | -------------------------------------- | --------- | -------- | -------- |
| Light | `--foreground` on `--background`       | 4.5:1     | 19.793:1 | Pass     |
| Light | `--primary-foreground` on `--primary`  | 4.5:1     | 7.216:1  | Pass     |
| Light | `--muted-foreground` on `--background` | 4.5:1     | 4.732:1  | Pass     |
| Light | `--ring` on `--background`             | 3:1       | 2.593:1  | **Fail** |
| Dark  | `--foreground` on `--background`       | 4.5:1     | 18.958:1 | Pass     |
| Dark  | `--primary-foreground` on `--primary`  | 4.5:1     | 9.022:1  | Pass     |
| Dark  | `--muted-foreground` on `--background` | 4.5:1     | 7.633:1  | Pass     |
| Dark  | `--ring` on `--background`             | 3:1       | 4.183:1  | Pass     |

Seven of the eight measurements pass. One fails.

## The failure

**Light-mode `--ring` on `--background` fails WCAG 1.4.11** (non-text contrast, 3:1 threshold): it measures **2.593:1**. Because the Sera style relies on the focus ring as the primary affordance for an underlined input rather than a secondary one on top of a visible box, this is exactly the pair the task was written to watch, and it is the one that comes up short.

No token has been adjusted to make this pass. Changing `--ring` by hand would reintroduce the drift that keeping the palette as preset tokens is meant to prevent, and the right fix — if one is wanted — is a decision (a different Theme colour with more contrast against white, most likely) rather than a mechanical edit to this file. That decision is left open for whoever owns the palette choice.
