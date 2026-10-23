import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { type ConsentAction, OAuthConsentCard } from './oauth-consent-card';

/** Mirrors `oauth-consent-card.test.tsx`'s own fixture. */
const consent = {
	authorizationId: '2f1c9d4e-7a2b-4c3d-8e5f-0123456789ab',
	clientName: 'kurze-url CLI',
	email: 'sabine@sv-gruenwald.example',
	kind: 'consent',
} as const;

const meta = {
	// Shared across every story below: nothing is in flight, unless a story says otherwise.
	args: { deciding: false, onDecide: fn<(action: ConsentAction) => void>() },
	component: OAuthConsentCard,
	title: 'Auth/OAuthConsentCard',
} satisfies Meta<typeof OAuthConsentCard>;

export default meta;

/** The request a person lands on from the terminal: who asks, for what, and Deny / Allow. */
export const Consent: StoryObj<typeof meta> = {
	args: { state: consent },
};

/** A decision is in flight (or the page is already leaving): both buttons are disabled. */
export const Deciding: StoryObj<typeof meta> = {
	args: { deciding: true, state: consent },
};

/** An expired, used or unknown authorization: the explanation, and nothing to press. */
export const Invalid: StoryObj<typeof meta> = {
	args: { state: { kind: 'invalid' } },
};

// German strings are reliably longer than English ones, and a `max-w-sm` card with two buttons
// side by side is where that would break the layout first.
export const ConsentGerman: StoryObj<typeof meta> = {
	args: { state: consent },
	globals: { language: 'de' },
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every story at its
// defaults, so the warning block's destructive tokens are checked in dark mode only by a story
// that sets it, the same reasoning as `link-scan-notice.stories.tsx`'s `Dark`.
export const Dark: StoryObj<typeof meta> = {
	args: { state: consent },
	globals: { theme: 'dark' },
};
