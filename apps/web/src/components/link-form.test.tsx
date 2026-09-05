import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { LinkForm, type LinkFormValues } from './link-form';

/**
 * Same pattern as `language-switcher.test.tsx`: any component that calls
 * `useTranslation` needs an `I18nextProvider` in its tree, or `t(...)` throws
 * looking up `react-i18next`'s default context. English is enough here — the
 * regexes below (`/redirect|weiterleitung/i`, `/destination|ziel/i`) already
 * match either language, and no test in this file asserts on German copy
 * specifically.
 */
function renderForm(props: {
	readonly fieldErrors?: Readonly<Record<string, string>>;
	readonly initial?: Partial<LinkFormValues>;
	readonly onSubmit: (values: LinkFormValues) => void;
}): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<LinkForm {...props} />
		</I18nextProvider>,
	);
}

describe('LinkForm', () => {
	it('warns inline when 301 is chosen', async () => {
		// CLAUDE.md requires this. A cached 301 stops clicks being counted and
		// stops later destination changes taking effect for anyone who has
		// visited once — breakage a user cannot diagnose and cannot undo.
		renderForm({ onSubmit: vi.fn() });
		await userEvent.selectOptions(screen.getByLabelText(/redirect|weiterleitung/i), '301');

		expect(await screen.findByRole('note')).toBeInTheDocument();
	});

	it('does not warn for 302', () => {
		renderForm({ onSubmit: vi.fn() });
		expect(screen.queryByRole('note')).not.toBeInTheDocument();
	});

	it('shows a server field error on the field it belongs to', () => {
		renderForm({ fieldErrors: { destination_url: 'must be https' }, onSubmit: vi.fn() });
		expect(screen.getByLabelText(/destination|ziel/i)).toHaveAccessibleDescription(/must be https/);
	});

	it('offers no domain picker', () => {
		// One domain exists on this instance, so a select with one option is
		// furniture. It appears when custom domains do.
		renderForm({ onSubmit: vi.fn() });
		expect(screen.queryByLabelText(/domain/i)).not.toBeInTheDocument();
	});
});
