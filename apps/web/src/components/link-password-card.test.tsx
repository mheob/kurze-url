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

	it('refuses a context-derived password without calling the server', async () => {
		const onSet = vi.fn();
		renderCard({ context, hasPassword: false, onRemove: vi.fn(), onSet });

		await userEvent.type(screen.getByLabelText('Password'), 'sommerfest2026');
		await userEvent.click(screen.getByRole('button', { name: 'Protect this link' }));

		expect(
			screen.getByText("Too easy to guess from this link, its destination, or the Verein's name."),
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
});
