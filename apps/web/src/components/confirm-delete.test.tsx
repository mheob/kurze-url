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
function renderWith(onConfirm: () => void): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<ConfirmDelete label="Delete" onConfirm={onConfirm} />
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
});
