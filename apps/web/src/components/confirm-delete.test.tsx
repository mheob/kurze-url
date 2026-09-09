import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { ConfirmDelete } from './confirm-delete';

/**
 * conventions.md: any component rendering something that calls
 * `useTranslation` needs an `I18nextProvider` in the tree — the plan's own
 * sample test for this component omitted it, which would fail at render
 * time, not merely produce untranslated text.
 */
const defaultQuestion = 'Delete this link? Anyone who already has the short URL will get a 404.';

function renderWith(
	onConfirm: () => void,
	question: string = defaultQuestion,
): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<ConfirmDelete label="Delete" onConfirm={onConfirm} question={question} />
		</I18nextProvider>,
	);
}

describe('ConfirmDelete', () => {
	/**
	 * Nothing restores a link, and its slug may already be in print on a
	 * flyer. One misclick must not be enough.
	 */
	it('does not delete on the first click', async () => {
		const onConfirm = vi.fn();
		renderWith(onConfirm);

		await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
		expect(onConfirm).not.toHaveBeenCalled();
	});

	it('deletes once confirmed', async () => {
		const onConfirm = vi.fn();
		renderWith(onConfirm);

		await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
		await userEvent.click(screen.getByRole('button', { name: 'Yes, delete it' }));
		expect(onConfirm).toHaveBeenCalledOnce();
	});

	/**
	 * `confirmLabel` is optional precisely so a caller that omits it — both
	 * existing callers, link delete and domain delete — keeps this exact
	 * wording. This is the behaviour a future change to the default would
	 * silently break.
	 */
	it('falls back to the generic confirm label when the caller does not override it', async () => {
		const onConfirm = vi.fn();
		renderWith(onConfirm);

		await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
		expect(screen.getByRole('button', { name: 'Yes, delete it' })).toBeInTheDocument();
	});

	/**
	 * A caller whose `onConfirm` does not delete anything — removing a link's
	 * password, not the link — supplies its own fully rendered string instead
	 * of the generic "Yes, delete it".
	 */
	it('shows the caller-supplied confirm label instead of the generic one', async () => {
		const onConfirm = vi.fn();
		render(
			<I18nextProvider i18n={createI18n('en')}>
				<ConfirmDelete
					confirmLabel="Yes, remove it"
					label="Remove protection"
					onConfirm={onConfirm}
					question="Remove the password? Anyone with the short URL will get straight through afterwards."
				/>
			</I18nextProvider>,
		);

		await userEvent.click(screen.getByRole('button', { name: 'Remove protection' }));
		expect(screen.getByRole('button', { name: 'Yes, remove it' })).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Yes, delete it' })).not.toBeInTheDocument();
	});

	/** The armed state must read as a labelled alert dialog, not a bare paragraph. */
	it('names the confirmation prompt on the alertdialog', async () => {
		const onConfirm = vi.fn();
		renderWith(onConfirm);

		await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
		expect(
			screen.getByRole('alertdialog', {
				name: 'Delete this link? Anyone who already has the short URL will get a 404.',
			}),
		).toBeInTheDocument();
	});

	/**
	 * `question` is a prop, not text this component invents itself — it is
	 * about to be reused by the team domains screen for a delete with an
	 * entirely different consequence than a link's, and a hardcoded link
	 * sentence would be wrong there.
	 */
	it('renders whatever question the caller supplies, not a fixed one', async () => {
		const onConfirm = vi.fn();
		renderWith(onConfirm, 'Delete links.verein.test? You will need to verify it again.');

		await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
		expect(
			screen.getByRole('alertdialog', {
				name: 'Delete links.verein.test? You will need to verify it again.',
			}),
		).toBeInTheDocument();
	});
});
