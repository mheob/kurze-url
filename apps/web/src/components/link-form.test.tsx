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

	it('shows a server field error for expires_at', () => {
		// Finding 1: this field previously had no `aria-describedby`/error `<p>`
		// wiring at all, so a rejection naming it (a past expiry, say) rendered
		// nothing — no field message, no banner, a silent failure.
		renderForm({ fieldErrors: { expires_at: 'must be in the future' }, onSubmit: vi.fn() });
		expect(screen.getByLabelText(/expires|läuft ab/i)).toHaveAccessibleDescription(
			/must be in the future/,
		);
	});

	it('marks a field with a server error as aria-invalid, not only described-by', () => {
		// Minor 11: `aria-describedby` alone tells assistive tech there is *a*
		// description, not that the field failed validation — `aria-invalid` is
		// what a screen reader announces unprompted, without the visitor having
		// to go hunting for the description text.
		renderForm({ fieldErrors: { destination_url: 'must be https' }, onSubmit: vi.fn() });
		expect(screen.getByLabelText(/destination|ziel/i)).toHaveAttribute('aria-invalid', 'true');
	});

	it('does not mark a field invalid when it has no error', () => {
		renderForm({ onSubmit: vi.fn() });
		expect(screen.getByLabelText(/destination|ziel/i)).not.toHaveAttribute('aria-invalid');
	});

	it('announces a field-level server error as an alert', () => {
		// Minor 11, other half: the error `<p>` had no `role="alert"`, so a
		// screen reader only reached it by hunting, the same gap
		// `aria-invalid` closes for the field itself.
		renderForm({ fieldErrors: { destination_url: 'must be https' }, onSubmit: vi.fn() });
		expect(screen.getByRole('alert')).toHaveTextContent('must be https');
	});

	it('shows a server field error for a field the form has no input for', () => {
		// A field the API might add later, or any name this form doesn't render
		// a specific input for — must still surface, not vanish.
		renderForm({ fieldErrors: { some_future_field: 'not allowed' }, onSubmit: vi.fn() });
		expect(screen.getByRole('alert')).toHaveTextContent('not allowed');
	});

	it('shows the slug placeholder saying one will be generated', () => {
		renderForm({ onSubmit: vi.fn() });
		expect(screen.getByLabelText(/short path|kurzpfad/i)).toHaveAttribute(
			'placeholder',
			'Leave empty and one will be generated',
		);
	});

	it('offers no domain picker', () => {
		// One domain exists on this instance, so a select with one option is
		// furniture. It appears when custom domains do.
		renderForm({ onSubmit: vi.fn() });
		expect(screen.queryByLabelText(/domain/i)).not.toBeInTheDocument();
	});
});
