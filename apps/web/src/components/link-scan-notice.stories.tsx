import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { flaggedScan } from '../test/link-scan';
import { LinkScanNotice } from './link-scan-notice';

const meta = {
	component: LinkScanNotice,
	title: 'Links/LinkScanNotice',
} satisfies Meta<typeof LinkScanNotice>;

export default meta;

export const Phishing: StoryObj<typeof meta> = {
	args: { scan: flaggedScan(['SOCIAL_ENGINEERING']), state: 'flagged' },
};

export const HarmfulSoftware: StoryObj<typeof meta> = {
	args: { scan: flaggedScan(['MALWARE', 'UNWANTED_SOFTWARE']), state: 'flagged' },
};

/** Two categories: the generic heading over each category's sentence. */
export const Mixed: StoryObj<typeof meta> = {
	args: { scan: flaggedScan(['SOCIAL_ENGINEERING', 'MALWARE']), state: 'flagged' },
};

/** A threat type Google added after this app was written still gets a qualified text and a link. */
export const UnknownThreat: StoryObj<typeof meta> = {
	args: { scan: flaggedScan(['THREAT_TYPE_FROM_THE_FUTURE']), state: 'flagged' },
};

/** A flagged link whose verdict names no threat type: the generic qualified text, not an error. */
export const NoThreatTypes: StoryObj<typeof meta> = {
	args: { scan: flaggedScan([]), state: 'flagged' },
};

export const German: StoryObj<typeof meta> = {
	args: { ...Phishing.args },
	globals: { language: 'de' },
};

// The destructive tokens this notice draws on (`border-destructive/50`,
// `bg-destructive/10`) are checked in dark mode only by a story that sets it,
// the same reasoning as `short-url-notice.stories.tsx`'s `Dark`.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Phishing.args },
	globals: { theme: 'dark' },
};
