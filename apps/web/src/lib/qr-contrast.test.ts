import { describe, expect, it } from 'vitest';

import { hasEnoughQrContrast, MIN_QR_CONTRAST_RATIO, qrContrastRatio } from './qr-contrast';

describe('qrContrastRatio', () => {
	it('matches WCAG at both extremes', () => {
		expect(qrContrastRatio('#000000', '#ffffff')).toBeCloseTo(21, 2);
		expect(qrContrastRatio('#333333', '#333333')).toBeCloseTo(1, 2);
	});

	it('does not depend on the argument order', () => {
		expect(qrContrastRatio('#00008b', '#ffffff')).toBeCloseTo(
			qrContrastRatio('#ffffff', '#00008b'),
			6,
		);
	});

	it('reads a colour with or without the leading hash', () => {
		expect(qrContrastRatio('000000', 'ffffff')).toBeCloseTo(21, 2);
	});
});

describe('hasEnoughQrContrast', () => {
	/**
	 * The same two pairs `apps/api/internal/qr/contrast_test.go` pins, so a
	 * drift between the two implementations shows up as a failing test on
	 * whichever side moved rather than as a preview that disagrees with the
	 * download.
	 */
	it("accepts a Verein's dark blue on white", () => {
		expect(hasEnoughQrContrast('#003366', '#ffffff')).toBe(true);
	});

	it('refuses yellow on white', () => {
		expect(hasEnoughQrContrast('#ffd700', '#ffffff')).toBe(false);
	});

	it('refuses anything that is not a six-digit hex colour', () => {
		expect(hasEnoughQrContrast('#fff', '#ffffff')).toBe(false);
		expect(hasEnoughQrContrast('rebeccapurple', '#ffffff')).toBe(false);
	});

	it('exposes the threshold it enforces', () => {
		expect(MIN_QR_CONTRAST_RATIO).toBe(4.5);
	});
});
