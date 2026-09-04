import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import { LanguageSwitcher } from './language-switcher';

function renderWith(language: 'en' | 'de') {
	return render(
		<I18nextProvider i18n={createI18n(language)}>
			<LanguageSwitcher />
		</I18nextProvider>,
	);
}

describe('LanguageSwitcher', () => {
	it('labels itself in the active language', () => {
		renderWith('de');
		expect(screen.getByRole('group', { name: 'Sprache' })).toBeInTheDocument();
	});

	it('marks the active language as pressed', () => {
		renderWith('de');
		expect(screen.getByRole('button', { name: 'Deutsch' })).toHaveAttribute('aria-pressed', 'true');
		expect(screen.getByRole('button', { name: 'Englisch' })).toHaveAttribute(
			'aria-pressed',
			'false',
		);
	});

	it('writes the cookie when another language is chosen', async () => {
		renderWith('en');
		await userEvent.click(screen.getByRole('button', { name: 'German' }));
		expect(document.cookie).toContain('lang=de');
	});
});
