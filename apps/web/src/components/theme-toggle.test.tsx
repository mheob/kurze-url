import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { afterEach, describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import { ThemeToggle } from './theme-toggle';

function renderWith(theme: 'dark' | 'light') {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<ThemeToggle theme={theme} />
		</I18nextProvider>,
	);
}

describe('ThemeToggle', () => {
	// The component toggles a class on `document.documentElement` directly
	// (see the component's own docstring); jsdom keeps one document per test
	// file, so a class left over from one test would otherwise leak into the
	// next.
	afterEach(() => {
		document.documentElement.classList.remove('dark');
	});

	it('flips the class and its own accessible name back on a second click', async () => {
		const user = userEvent.setup();
		renderWith('light');

		// `theme` is only ever the loader's initial value — a real click never
		// re-renders this component with a new prop, so the control has to track
		// the flip itself rather than re-deriving `next` from a prop that never
		// changes.
		await user.click(screen.getByRole('button', { name: 'Switch to dark mode' }));
		expect(document.documentElement).toHaveClass('dark');

		const button = screen.getByRole('button', { name: 'Switch to light mode' });
		await user.click(button);
		expect(document.documentElement).not.toHaveClass('dark');
		expect(screen.getByRole('button', { name: 'Switch to dark mode' })).toBeInTheDocument();
	});
});
