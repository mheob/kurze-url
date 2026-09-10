import { describe, expect, it } from 'vitest';

import { qrSvgDataUrl, restyleQrSvg } from './qr-svg';

/** The exact shape `apps/api/internal/qr` emits: one background `<rect>`, one `<path>` of per-module subpaths, a viewBox and no width/height. */
const svg = [
	'<svg xmlns="http://www.w3.org/2000/svg" version="1.1" viewBox="0 0 37 37" stroke="none">',
	'\t<rect width="37" height="37" fill="#FFFFFF"/>',
	'\t<path d="M4,4h1v1h-1z M6,4h1v1h-1z" fill="#000000"/>',
	'</svg>',
	'',
].join('\n');

describe('restyleQrSvg', () => {
	/**
	 * The property the whole preview design rests on: neither colour changes
	 * the QR matrix, so the browser can recolour a document the API produced
	 * instead of generating a second one. A generator in TypeScript would
	 * drift from the Go one, and drift in an image means the preview shows
	 * something the download does not deliver.
	 */
	it('recolours without touching the path data', () => {
		const restyled = restyleQrSvg(svg, { background: '#fffff5', foreground: '#003366' });

		expect(restyled).toContain('#003366');
		expect(restyled).toContain('#fffff5');
		expect(restyled).toContain('M4,4h1v1h-1z M6,4h1v1h-1z');
		expect(restyled).not.toContain('#000000');
		expect(restyled).not.toContain('#FFFFFF');
	});

	it('keeps the viewBox, so the code still scales to whatever box it is put in', () => {
		expect(restyleQrSvg(svg, { background: '#ffffff', foreground: '#000000' })).toContain(
			'viewBox="0 0 37 37"',
		);
	});

	it('returns the document unchanged when it is not parseable as SVG', () => {
		expect(restyleQrSvg('not an svg', { background: '#ffffff', foreground: '#000000' })).toBe(
			'not an svg',
		);
	});
});

describe('qrSvgDataUrl', () => {
	/**
	 * A data URL in an `<img src>`, not `dangerouslySetInnerHTML`: the
	 * document comes from our own API, but an `<img>` cannot execute
	 * anything a future change might put in it, and it carries real `alt`
	 * text.
	 */
	it('produces an image data URL the browser can render', () => {
		const url = qrSvgDataUrl(svg);

		expect(url.startsWith('data:image/svg+xml,')).toBe(true);
		expect(decodeURIComponent(url.slice('data:image/svg+xml,'.length))).toContain('<svg');
	});
});
