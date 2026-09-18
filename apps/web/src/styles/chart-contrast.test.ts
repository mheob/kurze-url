import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

/**
 * WCAG 1.4.11 asks for 3:1 between a graphical object and its background. A
 * chart line is a graphical object, and the chart sits inside a Card, so the
 * background is --card and not --background.
 */
const MINIMUM_CONTRAST = 3;

// Vite statically rewrites a *literal* `new URL('./x', import.meta.url)` into
// a dev-server asset URL, which jsdom's global `URL` then resolves against
// `location` instead of the file on disk. Routing the path through a
// variable sidesteps that rewrite so this reads the real stylesheet.
const appCssPath = './app.css';
// oxlint-disable-next-line node/no-sync -- module-scope fixture read in a test file: there is no event loop yet to block, and nothing concurrent to lose by waiting.
const css = readFileSync(fileURLToPath(new URL(appCssPath, import.meta.url)), 'utf8');

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
	const lumA = luminance(a);
	const lumB = luminance(b);
	// Two values only, so pick the extremes directly rather than sorting:
	// oxlint's `unicorn/no-array-sort` requires `.toSorted()` over `.sort()`
	// project-wide, but `.toSorted()` is ES2023, outside apps/web/tsconfig.json's
	// `lib` (ES2022) — the same tension apps/web/src/lib/preferences.ts documents
	// and avoids the same way.
	const high = Math.max(lumA, lumB);
	const low = Math.min(lumA, lumB);
	return (high + 0.05) / (low + 0.05);
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
