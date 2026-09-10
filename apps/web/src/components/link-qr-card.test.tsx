import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { LinkQRCard, type LinkQRCardProps } from './link-qr-card';

/** The exact shape `apps/api/internal/qr` emits — see `qr-svg.test.ts`, which uses the same fixture. */
const svg = [
	'<svg xmlns="http://www.w3.org/2000/svg" version="1.1" viewBox="0 0 37 37" stroke="none">',
	'\t<rect width="37" height="37" fill="#FFFFFF"/>',
	'\t<path d="M4,4h1v1h-1z" fill="#000000"/>',
	'</svg>',
].join('\n');

/** Same pattern as `link-password-card.test.tsx`'s `renderCard`: `useTranslation` needs an `I18nextProvider` in the tree. */
function renderCard(props: Partial<LinkQRCardProps> = {}): ReturnType<typeof render> {
	const merged: LinkQRCardProps = {
		isLoading: false,
		onDownload: vi.fn().mockResolvedValue(undefined),
		svg,
		...props,
	};
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<LinkQRCard {...merged} />
		</I18nextProvider>,
	);
}

describe('LinkQRCard', () => {
	it('shows the preview once the document has arrived', () => {
		renderCard();

		const preview = screen.getByRole('img', { name: "Preview of this link's QR code" });
		expect(preview.getAttribute('src')).toContain('data:image/svg+xml,');
	});

	it('says so while the document is still on its way', () => {
		renderCard({ isLoading: true, svg: undefined });

		expect(screen.getByText('Loading the preview…')).toBeInTheDocument();
	});

	it('says so when the document never arrived', () => {
		renderCard({ isLoading: false, svg: undefined });

		expect(screen.getByText('The preview could not be loaded.')).toBeInTheDocument();
	});

	/**
	 * The control is hidden rather than disabled: a vector has no pixel size,
	 * so there is nothing for it to mean. The API answers 422 for the same
	 * combination — this is the other half of that, not a replacement for it.
	 */
	it('hides the size control while SVG is selected and shows it for PNG', async () => {
		renderCard();

		expect(screen.queryByLabelText('Size in pixels')).not.toBeInTheDocument();

		await userEvent.selectOptions(screen.getByLabelText('File format'), 'png');

		expect(screen.getByLabelText('Size in pixels')).toBeInTheDocument();
	});

	/**
	 * The point of fetching once: recolouring is local, so changing a colour
	 * must not ask the parent for anything. `onDownload` is the only call
	 * this component ever makes, and it happens on the download control
	 * alone.
	 */
	it('recolours the preview without asking for a new document', async () => {
		const onDownload = vi.fn().mockResolvedValue(undefined);
		renderCard({ onDownload });

		const before = screen.getByRole('img').getAttribute('src');
		// `fireEvent.change`, not `userEvent.type`: `<input type="color">` is
		// not an editable text field, so `userEvent.clear` throws on it and
		// typing into it does nothing. The change event is what a real colour
		// picker dispatches anyway.
		fireEvent.change(screen.getByLabelText('Code colour'), { target: { value: '#003366' } });

		const after = screen.getByRole('img').getAttribute('src');
		expect(after).not.toBe(before);
		expect(decodeURIComponent(after ?? '')).toContain('#003366');
		expect(onDownload).not.toHaveBeenCalled();
	});

	it('refuses a low-contrast pair before it asks for a download', async () => {
		const onDownload = vi.fn().mockResolvedValue(undefined);
		renderCard({ onDownload });

		fireEvent.change(screen.getByLabelText('Code colour'), { target: { value: '#ffd700' } });
		await userEvent.click(screen.getByRole('button', { name: 'Download' }));

		expect(screen.getByRole('alert')).toHaveTextContent('too close together');
		expect(onDownload).not.toHaveBeenCalled();
	});

	it('asks for the download with the chosen format, size and colours', async () => {
		const onDownload = vi.fn().mockResolvedValue(undefined);
		renderCard({ onDownload });

		await userEvent.selectOptions(screen.getByLabelText('File format'), 'png');
		const size = screen.getByLabelText('Size in pixels');
		await userEvent.clear(size);
		await userEvent.type(size, '1024');
		await userEvent.click(screen.getByRole('button', { name: 'Download' }));

		expect(onDownload).toHaveBeenCalledWith({
			background: 'ffffff',
			foreground: '000000',
			format: 'png',
			size: 1024,
		});
	});

	/** A reason the mirror did not predict still has to reach the reader — the same escape hatch `LinkPasswordCard`'s `rejection` prop is. */
	it('renders a rejection the API reported', () => {
		renderCard({ rejection: 'size_requires_png' });

		expect(screen.getByRole('alert')).toHaveTextContent('only applies to the PNG');
	});

	it('renders an unrecognised rejection through the generic message', () => {
		renderCard({ rejection: 'rejected' });

		expect(screen.getByRole('alert')).toHaveTextContent('cannot be used');
	});

	it('does not show the low-contrast warning with a good colour pair', async () => {
		const onDownload = vi.fn().mockResolvedValue(undefined);
		renderCard({ onDownload });

		await userEvent.click(screen.getByRole('button', { name: 'Download' }));

		expect(screen.queryByRole('alert')).not.toBeInTheDocument();
		expect(onDownload).toHaveBeenCalled();
	});
});
