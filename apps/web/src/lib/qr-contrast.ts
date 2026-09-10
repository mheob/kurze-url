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

/** WCAG's floor for normal text, not its looser 3:1 for graphics. A QR module is smaller than a glyph and is read by a phone camera, not an eye. */
export const MIN_QR_CONTRAST_RATIO = 4.5;

const HEX_COLOR = /^#?[0-9a-f]{6}$/i;

/** Returns the three sRGB channels as 0–255, or `null` for anything that is not `rrggbb`. */
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
 */
function linearizeChannel(value: number): number {
	const s = value / 255;
	return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

/** WCAG 2.1's relative luminance: sRGB channels linearised, then weighted. */
function relativeLuminance([r, g, b]: [number, number, number]): number {
	return 0.2126 * linearizeChannel(r) + 0.7152 * linearizeChannel(g) + 0.0722 * linearizeChannel(b);
}

/**
 * `(lighter + 0.05) / (darker + 0.05)`, so the argument order does not
 * matter. Returns `1` — the worst possible ratio — for an unparseable colour,
 * so a malformed value fails closed rather than passing the check by
 * accident.
 */
export function qrContrastRatio(a: string, b: string): number {
	const first = channels(a);
	const second = channels(b);
	if (!first || !second) return 1;

	const la = relativeLuminance(first);
	const lb = relativeLuminance(second);
	const lighter = Math.max(la, lb);
	const darker = Math.min(la, lb);
	return (lighter + 0.05) / (darker + 0.05);
}

export function hasEnoughQrContrast(foreground: string, background: string): boolean {
	return qrContrastRatio(foreground, background) >= MIN_QR_CONTRAST_RATIO;
}
