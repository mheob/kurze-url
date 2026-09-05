import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';

/**
 * `vi.hoisted` plus a narrowly-typed fake, the same mechanics
 * `server/auth.test.ts` and `server/session.test.ts` settled on: a bare
 * `const sendMagicLink = vi.fn(...)` referenced from `vi.mock`'s factory
 * happens to work here too, but `vi.hoisted` is what makes that not an
 * accident of hoisting order.
 */
const mocks = vi.hoisted(() => ({
	sendMagicLink: vi.fn<(input: { data: { email: string } }) => Promise<{ sent: true }>>(
		async () => ({ sent: true }),
	),
}));

vi.mock('../server/auth', () => ({ sendMagicLink: mocks.sendMagicLink }));

const { LoginForm } = await import('./login');

/**
 * `LoginForm` reads every string through `useTranslation`, which needs an
 * `I18nextProvider` in scope — without one, `t()` has no instance to draw
 * from and renders the raw key. `language-switcher.test.tsx` settled on the
 * same `createI18n` + `I18nextProvider` wrapper for the same reason.
 */
function renderLoginForm(): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<LoginForm />
		</I18nextProvider>,
	);
}

describe('LoginForm', () => {
	it('shows the same confirmation whatever the address', async () => {
		renderLoginForm();
		await userEvent.type(screen.getByLabelText(/email|e-mail/i), 'a@example.test');
		await userEvent.click(screen.getByRole('button', { name: /link/i }));

		expect(await screen.findByText(/on its way|unterwegs/i)).toBeInTheDocument();
	});

	it('labels the field, so it is reachable without a mouse', async () => {
		// An input with only a placeholder passes a visual review and fails a
		// screen reader. Accessibility is a CI gate here, not a preference.
		renderLoginForm();
		expect(screen.getByLabelText(/email|e-mail/i)).toBeInTheDocument();
	});
});
