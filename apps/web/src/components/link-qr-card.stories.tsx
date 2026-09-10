import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { LinkQRCard } from './link-qr-card';

/** Mirrors `link-qr-card.test.tsx`'s own fixture. */
const svg = [
	'<svg xmlns="http://www.w3.org/2000/svg" version="1.1" viewBox="0 0 37 37" stroke="none">',
	'\t<rect width="37" height="37" fill="#FFFFFF"/>',
	'\t<path d="M4,4h1v1h-1z M6,4h1v1h-1z M8,4h1v1h-1z" fill="#000000"/>',
	'</svg>',
].join('\n');

const meta = {
	component: LinkQRCard,
	title: 'Links/LinkQRCard',
} satisfies Meta<typeof LinkQRCard>;

export default meta;

/** The ordinary state: the document has arrived and the preview is live. */
export const Ready: StoryObj<typeof meta> = {
	args: { isLoading: false, onDownload: fn(), svg },
};

/** The one fetch is still in flight. */
export const Loading: StoryObj<typeof meta> = {
	args: { isLoading: true, onDownload: fn(), svg: undefined },
};

/** The fetch failed. The controls stay usable — a download can still succeed where a preview did not. */
export const PreviewUnavailable: StoryObj<typeof meta> = {
	args: { isLoading: false, onDownload: fn(), svg: undefined },
};

/**
 * A refusal the API reported. This is what puts the error-association wiring
 * (`role="alert"`, `aria-describedby`) in front of the a11y addon, the same
 * reasoning `link-password-card.stories.tsx`'s `WithRejection` gives.
 */
export const WithRejection: StoryObj<typeof meta> = {
	args: { isLoading: false, onDownload: fn(), rejection: 'low_contrast', svg },
};
