/**
 * Recolouring for the QR preview. This module does not generate QR codes and
 * must never start to: the browser restyles a document the API produced, and
 * that is the property the whole preview design rests on. A second generator
 * in TypeScript would drift from the Go one, and drift in an image means the
 * preview shows something the download does not deliver.
 *
 * Neither colour changes the matrix — colours are attributes, and the size is
 * a number in the viewport — so one fetch per link is enough for any number
 * of colour changes.
 */

interface QrColors {
	background: string;
	foreground: string;
}

/**
 * Sets the background `<rect>`'s and the module `<path>`'s `fill` attributes.
 *
 * Parsed with `DOMParser` rather than string-replaced: the fills are the only
 * two attributes that may change, and a regular expression over path data
 * that happens to contain a colour-shaped substring is exactly the kind of
 * silent corruption an image will not report. An unparseable document is
 * returned untouched, so a malformed response renders as a broken image
 * rather than as a plausible wrong one.
 *
 * @param svg - The QR code SVG document as returned by the API.
 * @param colors - The background and foreground fill colours to apply.
 * @param colors.background - The background fill colour.
 * @param colors.foreground - The module fill colour.
 * @returns The recoloured SVG, or `svg` unchanged if it could not be parsed as expected.
 */
export function restyleQrSvg(svg: string, { background, foreground }: QrColors): string {
	const parsed = new DOMParser().parseFromString(svg, 'image/svg+xml');
	if (parsed.querySelector('parsererror') || !parsed.documentElement) return svg;
	if (parsed.documentElement.nodeName !== 'svg') return svg;

	const rect = parsed.querySelector('rect');
	const path = parsed.querySelector('path');
	if (!rect || !path) return svg;

	rect.setAttribute('fill', background);
	path.setAttribute('fill', foreground);

	return new XMLSerializer().serializeToString(parsed.documentElement);
}

/**
 * An `<img src>` value. Percent-encoded rather than base64 so the payload
 * stays inspectable in devtools and needs no `btoa` round trip — the document
 * is ASCII, since go-qr emits no text nodes at all.
 *
 * @param svg - The QR code SVG document to embed.
 * @returns A percent-encoded `data:image/svg+xml` URL for `svg`.
 */
export function qrSvgDataUrl(svg: string): string {
	return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}
