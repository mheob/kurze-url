import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { fn } from 'storybook/test';

import { LinkPasswordCard } from './link-password-card';

/** Mirrors `link-password-card.test.tsx`'s own fixture. */
const context = {
	destinationUrl: 'https://www.sv-gruenwald.de/verein/sommerfest',
	linkSlug: 'sommerfest-2026',
	teamName: 'SV Grünwald e.V.',
	teamSlug: 'sv-gruenwald',
};

const meta = {
	component: LinkPasswordCard,
	title: 'Links/LinkPasswordCard',
} satisfies Meta<typeof LinkPasswordCard>;

export default meta;

/** A link with no password yet — the input is visible right away, nothing to reveal first. */
export const Unprotected: StoryObj<typeof meta> = {
	args: { context, hasPassword: false, onRemove: fn(), onSet: fn() },
};

/** A protected link: the input stays hidden behind "Change password" until asked for, and removal needs its own confirmation. */
export const Protected: StoryObj<typeof meta> = {
	args: { context, hasPassword: true, onRemove: fn(), onSet: fn() },
};

/**
 * A reason the API reported that the mirrored policy did not predict — this
 * is what puts the error-association wiring (`aria-describedby`,
 * `aria-invalid`) in front of the a11y addon, the same reasoning
 * `link-form.stories.tsx`'s own `WithFieldError` gives for its story.
 */
export const WithRejection: StoryObj<typeof meta> = {
	args: { context, hasPassword: false, onRemove: fn(), onSet: fn(), rejection: 'too_common' },
};

/**
 * `'rejected'` is the fallback for a reason token this build does not
 * recognise — the API-only case `validateLinkPassword` can never produce
 * locally, so it is the one value a dropped or mis-keyed `messageKeys` entry
 * would not be caught by `WithRejection` above alone.
 */
export const WithUnrecognizedRejection: StoryObj<typeof meta> = {
	args: { context, hasPassword: false, onRemove: fn(), onSet: fn(), rejection: 'rejected' },
};
