import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vite-plus/test';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import {
	type ConsentCardState,
	OAuthConsentCard,
	type OAuthConsentCardProps,
} from './oauth-consent-card';

const consent = {
	authorizationId: 'abc-123',
	clientName: 'kurze-url CLI',
	email: 'sabine@sv-gruenwald.example',
	kind: 'consent',
} as const;

const invalid = { kind: 'invalid' } as const;

/**
 * Same pattern as `link-scan-notice.test.tsx`: a component calling `useTranslation` needs an
 * `I18nextProvider`, or `t(...)` renders the raw key.
 *
 * @param props - The props this test cares about; the rest is a card nobody is deciding on.
 * @param language - Which catalogue to load.
 * @returns Testing Library's render result.
 */
function renderCard(
	props: Partial<OAuthConsentCardProps>,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n(language)}>
			<OAuthConsentCard
				deciding={false}
				onDecide={vi.fn<OAuthConsentCardProps['onDecide']>()}
				state={consent}
				{...props}
			/>
		</I18nextProvider>,
	);
}

describe(OAuthConsentCard, () => {
	it('names the client and what it would be given, under the title as the only heading', () => {
		renderCard({});

		// The title is the page's only heading: `CardTitle` is a `<div>`, so the card nests a real
		// `<h1>` in it, as the login page does.
		expect(screen.getAllByRole('heading')).toHaveLength(1);
		expect(screen.getByRole('heading', { level: 1, name: 'Allow CLI access' })).toBeInTheDocument();
		expect(
			screen.getByText('kurze-url CLI wants to access kurze-url on your behalf.'),
		).toBeInTheDocument();
		expect(
			screen.getByText(
				'It can then do everything you can do in the web app, in every team you belong to.',
			),
		).toBeInTheDocument();
	});

	it('warns, and says who is signed in', () => {
		renderCard({});

		expect(screen.getByRole('note')).toHaveTextContent(
			'Only allow this if you just ran kurze-url login in your terminal yourself.',
		);
		expect(screen.getByText('Signed in as sabine@sv-gruenwald.example')).toBeInTheDocument();
	});

	it('offers Deny before Allow, both enabled, so the default focus order never lands on approval first', () => {
		renderCard({});

		const [first, second, ...rest] = screen.getAllByRole('button');
		expect(first).toHaveAccessibleName('Deny');
		expect(second).toHaveAccessibleName('Allow');
		expect(rest).toHaveLength(0);
		expect(first).toBeEnabled();
		expect(second).toBeEnabled();
	});

	it.each([consent, invalid] as readonly ConsentCardState[])(
		'puts everything inside one main landmark, for the %j state',
		(state) => {
			renderCard({ state });

			expect(screen.getAllByRole('main')).toHaveLength(1);
			expect(screen.getByRole('main')).toContainElement(screen.getByRole('heading'));
		},
	);

	it('reports Allow as an approval', async () => {
		const onDecide = vi.fn<OAuthConsentCardProps['onDecide']>();
		renderCard({ onDecide });

		await userEvent.click(screen.getByRole('button', { name: 'Allow' }));

		expect(onDecide).toHaveBeenCalledExactlyOnceWith('approve');
	});

	it('reports Deny as a denial', async () => {
		const onDecide = vi.fn<OAuthConsentCardProps['onDecide']>();
		renderCard({ onDecide });

		await userEvent.click(screen.getByRole('button', { name: 'Deny' }));

		expect(onDecide).toHaveBeenCalledExactlyOnceWith('deny');
	});

	// Review Focus 3: a double click on Allow must send one decision, and the card cannot count
	// clicks itself, so what it owes the route is two disabled buttons while a decision is pending.
	it('disables both buttons while a decision is pending', async () => {
		const onDecide = vi.fn<OAuthConsentCardProps['onDecide']>();
		renderCard({ deciding: true, onDecide });

		expect(screen.getByRole('button', { name: 'Allow' })).toBeDisabled();
		expect(screen.getByRole('button', { name: 'Deny' })).toBeDisabled();

		await userEvent.click(screen.getByRole('button', { name: 'Allow' }));
		await userEvent.click(screen.getByRole('button', { name: 'Deny' }));

		expect(onDecide).not.toHaveBeenCalled();
	});

	it('shows only the explanation, and no buttons, for a request that cannot be completed', () => {
		renderCard({ state: invalid });

		expect(screen.getAllByRole('heading')).toHaveLength(1);
		expect(
			screen.getByRole('heading', { level: 1, name: "This request can't be completed" }),
		).toBeInTheDocument();
		expect(
			screen.getByText(
				'The link has expired or is not valid. Run kurze-url login in your terminal again.',
			),
		).toBeInTheDocument();
		expect(screen.queryByRole('button')).not.toBeInTheDocument();
		expect(screen.queryByRole('note')).not.toBeInTheDocument();
	});

	it('reads in German', () => {
		renderCard({}, 'de');

		expect(screen.getByRole('heading', { name: 'CLI-Zugriff erlauben' })).toBeInTheDocument();
		expect(screen.getByText('Angemeldet als sabine@sv-gruenwald.example')).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Erlauben' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Ablehnen' })).toBeInTheDocument();
	});

	// The client's name is whatever the client registered, so it is text, never markup.
	it('renders a client name as text, whatever it contains', () => {
		renderCard({ state: { ...consent, clientName: '<img src=x onerror=alert(1)>' } });

		expect(
			screen.getByText('<img src=x onerror=alert(1)> wants to access kurze-url on your behalf.'),
		).toBeInTheDocument();
		expect(document.querySelector('img')).toBeNull();
	});
});
