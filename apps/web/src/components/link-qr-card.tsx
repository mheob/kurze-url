import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { QrRejectionReason } from '../lib/api-errors';
import { hasEnoughQrContrast, isValidQrColor } from '../lib/qr-contrast';
import { qrSvgDataUrl, restyleQrSvg } from '../lib/qr-svg';
import { Button } from './ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card';
import { Field, FieldDescription, FieldError, FieldLabel } from './ui/field';
import { Input } from './ui/input';
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from './ui/input-group';

/**
 * Every reason the mirrored rule or the API's typed 422 can carry, mapped to
 * its translation key as a `Record` rather than a lookup function — adding a
 * reason to `QrRejectionReason` without adding it here is a compile error,
 * not a blank message a reader has no way to act on. Same shape as
 * `link-password-card.tsx`'s `messageKeys`.
 */
const messageKeys: Record<QrRejectionReason | 'rejected', string> = {
	invalid_color: 'links.qrInvalidColor',
	low_contrast: 'links.qrLowContrast',
	rejected: 'links.qrRejected',
	size_requires_png: 'links.qrSizeRequiresPng',
};

/**
 * The API's own defaults and bounds (`apps/api/internal/qr/qr.go`), repeated
 * here so the controls start where the endpoint would and refuse what it
 * would refuse. A drift costs one rejected request with a message under the
 * control, not a wrong image — the endpoint validates these regardless.
 */
const DEFAULT_FOREGROUND = '#000000';
const DEFAULT_BACKGROUND = '#ffffff';
const DEFAULT_SIZE = 512;
const MIN_SIZE = 64;
const MAX_SIZE = 2048;

/** The preview's own box, in CSS pixels. Small sizes render smaller so the control's effect is visible; large ones stop here rather than filling the page. */
const MAX_PREVIEW_PIXELS = 240;

/**
 * The API takes `rrggbb`: a raw `#` in a query string is the fragment delimiter and never reaches the server. The field's own leading-`#` addon is only ever shown, never typed, but the value is normalised on the way in too, so a pasted `#003366` and a typed `003366` end up in state the same way.
 *
 * @param color - The colour string, possibly still carrying a leading `#`.
 * @returns The colour with any leading `#` stripped.
 */
function bare(color: string): string {
	return color.startsWith('#') ? color.slice(1) : color;
}

export interface LinkQRCardProps {
	/** True while the one SVG fetch is in flight. `svg` undefined with this false means the fetch failed. */
	readonly isLoading: boolean;
	/** Called when the reader changes a control, so a stale API-reported `rejection` does not linger over a combination they are already correcting. */
	readonly onDismissRejection?: () => void;
	/** Resolves when the download has been handed to the browser, rejects on failure. Colours are sent as bare `rrggbb`. */
	readonly onDownload: (options: {
		readonly background: string;
		readonly foreground: string;
		readonly format: QrFormat;
		readonly size: number;
	}) => Promise<void>;
	/** A reason the API returned that the mirrored contrast rule did not predict. */
	readonly rejection?: QrRejectionReason | 'rejected';
	/** The document fetched once for this link, in the default colours. */
	readonly svg: string | undefined;
}

/** Exported because `LinkQRCardProps` names it: an unexported type in a public prop makes the prop unnameable from a parent. */
export type QrFormat = 'png' | 'svg';

/**
 * The card that turns a link into something a Verein can print.
 *
 * It fetches nothing and generates nothing. The parent hands it one SVG
 * document the API produced; every colour change recolours that document in
 * place, because neither colour nor size changes the QR matrix. The API is
 * called again only for the download, through `onDownload`.
 *
 * That is the property worth protecting: a second QR generator in TypeScript
 * would drift from the Go one, and drift in an image means the preview shows
 * something the download does not deliver.
 *
 * @param props - The component's props.
 * @param props.isLoading - True while the one SVG fetch is in flight.
 * @param props.onDismissRejection - Called when a control changes, to clear a stale `rejection`.
 * @param props.onDownload - Requests the download for the chosen format/colours/size; colours are passed without a leading `#`.
 * @param props.rejection - A reason the API returned that the mirrored contrast rule did not predict.
 * @param props.svg - The document fetched once for this link, in the default colours.
 * @returns The rendered QR card section.
 */
export function LinkQRCard({
	isLoading,
	onDismissRejection,
	onDownload,
	rejection,
	svg,
}: LinkQRCardProps): React.JSX.Element {
	const { t } = useTranslation();
	const formatId = useId();
	const foregroundId = useId();
	const backgroundId = useId();
	const sizeId = useId();
	const sizeHintId = useId();
	const errorId = useId();

	const [format, setFormat] = useState<QrFormat>('svg');
	const [foreground, setForeground] = useState(DEFAULT_FOREGROUND);
	const [background, setBackground] = useState(DEFAULT_BACKGROUND);
	const [size, setSize] = useState(DEFAULT_SIZE);
	// The reason the *local* check found, distinct from `rejection` (the
	// API's own finding): changing a control clears this and asks the parent
	// to clear its half, so a stale message does not linger over a
	// combination the reader has already corrected.
	const [localReason, setLocalReason] = useState<QrRejectionReason | null>(null);

	const reason = localReason ?? rejection;
	const message = reason ? t(messageKeys[reason]) : undefined;

	// `size` is kept raw in state so the field doesn't fight the reader
	// mid-edit — clearing it to retype is ordinary, and `Number('')` is `0`.
	// Clamping happens here, at the point of use, not on every keystroke:
	// an empty or zero field falls back to the default before the min/max
	// clamp, so it can never reach the preview's `<img>` dimensions or a
	// download request as `0` — a `0` would make the preview vanish
	// mid-edit and would make the API reject the download outright.
	const requestedSize = Math.min(Math.max(size || DEFAULT_SIZE, MIN_SIZE), MAX_SIZE);

	function changed(): void {
		setLocalReason(null);
		onDismissRejection?.();
	}

	async function handleDownload(): Promise<void> {
		// Format first, contrast second: `hasEnoughQrContrast` already fails
		// closed on an unparseable colour (a ratio of `1`, always below the
		// threshold), so without this ordering a malformed hex value would
		// surface as `qrLowContrast` instead of the more specific
		// `qrInvalidColor` the free-text field now makes reachable.
		if (!isValidQrColor(foreground) || !isValidQrColor(background)) {
			setLocalReason('invalid_color');
			return;
		}
		if (!hasEnoughQrContrast(foreground, background)) {
			setLocalReason('low_contrast');
			return;
		}
		setLocalReason(null);
		try {
			await onDownload({
				background: bare(background),
				foreground: bare(foreground),
				format,
				size: requestedSize,
			});
		} catch {
			// The parent classifies why and feeds a reason back through
			// `rejection`, or renders its own banner for a failure that is not
			// a QR refusal at all. Nothing to do here.
		}
	}

	const previewSize =
		format === 'png' ? Math.min(requestedSize, MAX_PREVIEW_PIXELS) : MAX_PREVIEW_PIXELS;
	const preview = svg !== undefined ? restyleQrSvg(svg, { background, foreground }) : undefined;

	return (
		<Card>
			<CardHeader>
				{/* `CardTitle` hardcodes a `<div>` — see the same note on
				    `link-password-card.tsx`'s `CardTitle`: a real nested `<h2>`
				    keeps this in the page's heading structure without fighting
				    `jsx-a11y/prefer-tag-over-role`, and without editing `ui/card.tsx`. */}
				<CardTitle>
					<h2>{t('links.qrHeading')}</h2>
				</CardTitle>
				<CardDescription>{t('links.qrExplainer')}</CardDescription>
			</CardHeader>
			<CardContent>
				{preview !== undefined ? (
					<img
						alt={t('links.qrPreviewAlt')}
						height={previewSize}
						src={qrSvgDataUrl(preview)}
						width={previewSize}
					/>
				) : (
					<p>{t(isLoading ? 'links.qrPreviewLoading' : 'links.qrPreviewUnavailable')}</p>
				)}

				{/* Not converted to the design system's `Select`: see the same note
				    on `redirect_type` in `link-form.tsx` — a popup listbox would stop
				    `userEvent.selectOptions` from working below and would change how
				    the control is actually operated. */}
				<Field>
					<FieldLabel htmlFor={formatId}>{t('links.qrFormat')}</FieldLabel>
					<select
						id={formatId}
						onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
							setFormat(event.target.value === 'png' ? 'png' : 'svg');
							changed();
						}}
						value={format}
					>
						<option value="svg">{t('links.qrFormatSvg')}</option>
						<option value="png">{t('links.qrFormatPng')}</option>
					</select>
				</Field>

				<Field>
					<FieldLabel htmlFor={foregroundId}>{t('links.qrForeground')}</FieldLabel>
					<InputGroup>
						<InputGroupAddon>
							<InputGroupText>{t('links.qrColorPrefix')}</InputGroupText>
						</InputGroupAddon>
						<InputGroupInput
							id={foregroundId}
							maxLength={6}
							onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
								setForeground(`#${bare(event.target.value)}`);
								changed();
							}}
							value={bare(foreground)}
						/>
					</InputGroup>
				</Field>

				<Field>
					<FieldLabel htmlFor={backgroundId}>{t('links.qrBackground')}</FieldLabel>
					<InputGroup>
						<InputGroupAddon>
							<InputGroupText>{t('links.qrColorPrefix')}</InputGroupText>
						</InputGroupAddon>
						<InputGroupInput
							id={backgroundId}
							maxLength={6}
							onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
								setBackground(`#${bare(event.target.value)}`);
								changed();
							}}
							value={bare(background)}
						/>
					</InputGroup>
				</Field>

				{format === 'png' ? (
					<Field>
						<FieldLabel htmlFor={sizeId}>{t('links.qrSize')}</FieldLabel>
						<Input
							aria-describedby={sizeHintId}
							id={sizeId}
							max={MAX_SIZE}
							min={MIN_SIZE}
							onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
								setSize(Number(event.target.value));
								changed();
							}}
							type="number"
							value={size}
						/>
						<FieldDescription id={sizeHintId}>{t('links.qrSizeHint')}</FieldDescription>
					</Field>
				) : null}

				{message === undefined ? null : <FieldError id={errorId}>{message}</FieldError>}

				<Button
					aria-describedby={message !== undefined ? errorId : undefined}
					onClick={() => {
						void handleDownload();
					}}
					type="button"
				>
					{t('links.qrDownload')}
				</Button>
			</CardContent>
		</Card>
	);
}
