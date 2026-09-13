/**
 * A browser-side copy of the contrast rule that
 * `apps/api/internal/qr/contrast.go` enforces, so a colour pair a camera
 * cannot read is refused before a request goes out.
 *
 * Unlike `link-password.ts`, this mirror is not expected to drift: the
 * password policy mirrors a curated word list, and this mirrors a closed
 * formula out of WCAG 2.1. There is nothing here to curate. The API remains
 * the enforcement point either way — a 422 still renders under the control.
 */

const HEX_COLOR = /^#?[0-9a-f]{6}$/iu;

/** The largest value an 8-bit sRGB channel can hold, for normalising 0–255 down to 0–1. */
const SRGB_CHANNEL_MAX = 255;

/** WCAG 2.1's threshold between the linear and gamma-corrected segments of the sRGB transfer function. */
const SRGB_LINEAR_THRESHOLD = 0.03928;
/** WCAG 2.1's divisor for the linear segment (s <= SRGB_LINEAR_THRESHOLD). */
const SRGB_LINEAR_DIVISOR = 12.92;
/** WCAG 2.1's offset in the gamma-corrected segment's `(s + offset) / divisor` term. */
const SRGB_GAMMA_OFFSET = 0.055;
/** WCAG 2.1's divisor in the gamma-corrected segment's `(s + offset) / divisor` term. */
const SRGB_GAMMA_DIVISOR = 1.055;
/** WCAG 2.1's exponent applied to the gamma-corrected segment. */
const SRGB_GAMMA_EXPONENT = 2.4;

/** WCAG 2.1's red-channel weight in the relative luminance formula. */
const LUMINANCE_RED_COEFFICIENT = 0.2126;
/** WCAG 2.1's green-channel weight in the relative luminance formula. */
const LUMINANCE_GREEN_COEFFICIENT = 0.7152;
/** WCAG 2.1's blue-channel weight in the relative luminance formula. */
const LUMINANCE_BLUE_COEFFICIENT = 0.0722;

/** WCAG 2.1's `+0.05` added to both luminances in the contrast ratio formula, so neither side is ever divided by (or added to) zero. */
const CONTRAST_RATIO_OFFSET = 0.05;

/**
 * Returns the three sRGB channels as 0–255, or `null` for anything that is not `rrggbb`.
 *
 * @param raw - A hex colour, with or without a leading `#`.
 * @returns The `[r, g, b]` channels, or `null` if `raw` is not a valid 6-digit hex colour.
 */
function channels(raw: string): [number, number, number] | null {
	if (!HEX_COLOR.test(raw)) return null;
	const digits = raw.startsWith('#') ? raw.slice(1) : raw;
	return [
		Number.parseInt(digits.slice(0, 2), 16),
		Number.parseInt(digits.slice(2, 4), 16),
		Number.parseInt(digits.slice(4, 6), 16),
	];
}

/**
 * Linearises one sRGB channel (0–255), per WCAG 2.1. Hoisted out of
 * `relativeLuminance` rather than nested there: it captures nothing from that
 * scope, so oxlint's `unicorn/consistent-function-scoping` refuses a closure
 * that would otherwise be recreated on every call for no reason.
 *
 * @param value - One sRGB channel, 0–255.
 * @returns The linearised channel value.
 */
function linearizeChannel(value: number): number {
	const s = value / SRGB_CHANNEL_MAX;
	return s <= SRGB_LINEAR_THRESHOLD
		? s / SRGB_LINEAR_DIVISOR
		: ((s + SRGB_GAMMA_OFFSET) / SRGB_GAMMA_DIVISOR) ** SRGB_GAMMA_EXPONENT;
}

/**
 * WCAG 2.1's relative luminance: sRGB channels linearised, then weighted.
 *
 * @param rgb - The `[r, g, b]` sRGB channels, each 0–255.
 * @param rgb.0 - The red channel.
 * @param rgb.1 - The green channel.
 * @param rgb.2 - The blue channel.
 * @returns The relative luminance.
 */
function relativeLuminance([r, g, b]: readonly [number, number, number]): number {
	return (
		LUMINANCE_RED_COEFFICIENT * linearizeChannel(r) +
		LUMINANCE_GREEN_COEFFICIENT * linearizeChannel(g) +
		LUMINANCE_BLUE_COEFFICIENT * linearizeChannel(b)
	);
}

/** WCAG's floor for normal text, not its looser 3:1 for graphics. A QR module is smaller than a glyph and is read by a phone camera, not an eye. */
export const MIN_QR_CONTRAST_RATIO = 4.5;

/**
 * `(lighter + 0.05) / (darker + 0.05)`, so the argument order does not
 * matter. Returns `1` — the worst possible ratio — for an unparseable colour,
 * so a malformed value fails closed rather than passing the check by
 * accident.
 *
 * @param a - One of the two colours, as `rrggbb`.
 * @param b - The other colour, as `rrggbb`.
 * @returns The WCAG contrast ratio between `a` and `b`, or `1` if either is unparseable.
 */
export function qrContrastRatio(a: string, b: string): number {
	const first = channels(a);
	const second = channels(b);
	if (!first || !second) return 1;

	const la = relativeLuminance(first);
	const lb = relativeLuminance(second);
	const lighter = Math.max(la, lb);
	const darker = Math.min(la, lb);
	return (lighter + CONTRAST_RATIO_OFFSET) / (darker + CONTRAST_RATIO_OFFSET);
}

export function hasEnoughQrContrast(foreground: string, background: string): boolean {
	return qrContrastRatio(foreground, background) >= MIN_QR_CONTRAST_RATIO;
}
