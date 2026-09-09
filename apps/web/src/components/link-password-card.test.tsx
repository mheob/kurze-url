import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { LinkPasswordCard, type LinkPasswordCardProps } from './link-password-card';

const context = {
	destinationUrl: 'https://www.sv-gruenwald.de/verein/sommerfest',
	linkSlug: 'sommerfest-2026',
	teamName: 'SV Grünwald e.V.',
	teamSlug: 'sv-gruenwald',
};

/** Same pattern as `link-form.test.tsx`'s `renderForm`: `useTranslation` needs an `I18nextProvider` in the tree. */
function renderCard(props: LinkPasswordCardProps): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<LinkPasswordCard {...props} />
		</I18nextProvider>,
	);
}

describe('LinkPasswordCard', () => {
	it('offers to protect an unprotected link', () => {
		renderCard({ context, hasPassword: false, onRemove: vi.fn(), onSet: vi.fn() });

		expect(screen.getByText('This link is not protected.')).toBeInTheDocument();
	});

	it('reports a protected link and offers removal', () => {
		renderCard({ context, hasPassword: true, onRemove: vi.fn(), onSet: vi.fn() });

		expect(screen.getByText('This link is protected by a password.')).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Remove protection' })).toBeInTheDocument();
	});

	/**
	 * Removing a password deletes nothing, so its confirm control must not
	 * read as "Yes, delete it" — that would offer to delete the link itself.
	 * `ConfirmDelete`'s `confirmLabel` override is what keeps this specific.
	 */
	it('labels the removal confirmation with password-specific text, not the generic delete text', async () => {
		renderCard({ context, hasPassword: true, onRemove: vi.fn(), onSet: vi.fn() });

		await userEvent.click(screen.getByRole('button', { name: 'Remove protection' }));

		expect(screen.getByRole('button', { name: 'Yes, remove it' })).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Yes, delete it' })).not.toBeInTheDocument();
	});

	it('refuses a context-derived password without calling the server', async () => {
		const onSet = vi.fn();
		renderCard({ context, hasPassword: false, onRemove: vi.fn(), onSet });

		await userEvent.type(screen.getByLabelText('Password'), 'sommerfest2026');
		await userEvent.click(screen.getByRole('button', { name: 'Protect this link' }));

		expect(
			screen.getByText(
				"Too easy to guess from this link, its destination, or the Verein's name. Choose something unrelated to all three.",
			),
		).toBeInTheDocument();
		expect(onSet).not.toHaveBeenCalled();
	});

	it('submits a password that passes the mirrored policy', async () => {
		const onSet = vi.fn();
		renderCard({ context, hasPassword: false, onRemove: vi.fn(), onSet });

		await userEvent.type(screen.getByLabelText('Password'), 'Kartoffelsalat!7');
		await userEvent.click(screen.getByRole('button', { name: 'Protect this link' }));

		expect(onSet).toHaveBeenCalledWith('Kartoffelsalat!7');
	});

	/**
	 * The bug this fix round exists for: `changing` and `password` had no
	 * reset path, so a successful change-in-place left the editor open with
	 * the just-submitted password still sitting in the input. `onSet` now
	 * returns a promise the card awaits — resolving it is the parent's way of
	 * signalling success back into the card, which owns this state.
	 */
	it('closes the editor and clears the field after successfully changing an existing password', async () => {
		const onSet = vi.fn().mockResolvedValue(undefined);
		renderCard({ context, hasPassword: true, onRemove: vi.fn(), onSet });

		await userEvent.click(screen.getByRole('button', { name: 'Change password' }));
		await userEvent.type(screen.getByLabelText('Password'), 'Kartoffelsalat!7');
		await userEvent.click(screen.getByRole('button', { name: 'Change password' }));

		expect(onSet).toHaveBeenCalledWith('Kartoffelsalat!7');
		// The editor closes: the password input is gone, and the
		// "protected"/Change/Remove view is back — with no leftover value.
		expect(screen.queryByLabelText('Password')).not.toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Change password' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Remove protection' })).toBeInTheDocument();
	});

	/**
	 * The failure mirror of the test above: a rejected submit must not clear
	 * the field, or the reader has to retype a password that was just
	 * refused for a reason they may not even see yet (a slow network, a rate
	 * limit) rather than one they can fix by editing.
	 */
	it('keeps the editor open with the typed password after a rejected change', async () => {
		const onSet = vi.fn().mockRejectedValue(new Error('rejected'));
		renderCard({ context, hasPassword: true, onRemove: vi.fn(), onSet });

		await userEvent.click(screen.getByRole('button', { name: 'Change password' }));
		await userEvent.type(screen.getByLabelText('Password'), 'Kartoffelsalat!7');
		await userEvent.click(screen.getByRole('button', { name: 'Change password' }));

		expect(onSet).toHaveBeenCalledWith('Kartoffelsalat!7');
		expect(screen.getByLabelText('Password')).toHaveValue('Kartoffelsalat!7');
	});

	// The mirrored policy is convenience; the API is truth. A reason the
	// browser did not predict still has to reach the reader.
	it('renders a rejection the API reported', () => {
		renderCard({
			context,
			hasPassword: false,
			onRemove: vi.fn(),
			onSet: vi.fn(),
			rejection: 'too_common',
		});

		expect(
			screen.getByText('That password is one of the most common ones. Pick another.'),
		).toBeInTheDocument();
	});

	/**
	 * `'rejected'` is the fallback for a reason token this build does not
	 * recognise — the one value `validateLinkPassword` itself can never
	 * produce, so a `messageKeys` entry that got dropped or mis-keyed would
	 * still compile and pass every other test. This is the one that pins it.
	 */
	it('renders the fallback message for a reason the mirror cannot predict', () => {
		renderCard({
			context,
			hasPassword: false,
			onRemove: vi.fn(),
			onSet: vi.fn(),
			rejection: 'rejected',
		});

		expect(screen.getByText('That password cannot be used. Pick another.')).toBeInTheDocument();
	});

	/**
	 * Before this fix, `changing` had no way back to `false` other than a
	 * successful submit: `<ConfirmDelete>` unmounted the moment "Change
	 * password" was clicked, and nothing else set `changing` back. A reader
	 * who clicked it by mistake had no way out short of navigating away.
	 */
	it('cancels out of the change-password editor back to the protected view', async () => {
		const onSet = vi.fn();
		renderCard({ context, hasPassword: true, onRemove: vi.fn(), onSet });

		await userEvent.click(screen.getByRole('button', { name: 'Change password' }));
		await userEvent.type(screen.getByLabelText('Password'), 'Kartoffelsalat!7');
		await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));

		expect(onSet).not.toHaveBeenCalled();
		expect(screen.queryByLabelText('Password')).not.toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Change password' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Remove protection' })).toBeInTheDocument();
	});

	/**
	 * The other half of "a fresh keystroke clears a stale message": `onChange`
	 * cleared `localReason` but left an API-reported `rejection` rendered,
	 * `aria-invalid` still true, over a password the reader was in the middle
	 * of correcting. `onDismissRejection` is the card's way of asking the
	 * parent — the owner of `rejection` — to clear it too.
	 */
	it('asks the parent to dismiss a stale API-reported rejection when the reader edits the field', async () => {
		const onDismissRejection = vi.fn();
		renderCard({
			context,
			hasPassword: false,
			onDismissRejection,
			onRemove: vi.fn(),
			onSet: vi.fn(),
			rejection: 'too_common',
		});

		await userEvent.type(screen.getByLabelText('Password'), 'x');

		expect(onDismissRejection).toHaveBeenCalledTimes(1);
	});
});
