import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import { SiteFooter } from './site-footer';

function renderFooter(apiStatus: string): void {
	render(
		<I18nextProvider i18n={createI18n('en')}>
			<SiteFooter apiStatus={apiStatus} />
		</I18nextProvider>,
	);
}

describe('SiteFooter', () => {
	it('exposes the API status as data, not only as prose', () => {
		// `e2e/global-setup.ts` reads this attribute to decide whether the
		// deployment is wired to a real API before running a single spec. The
		// prose next to it is translated and would make that check depend on the
		// visitor's language; the attribute carries the untranslated technical
		// value. Dropping it would not break any page, which is exactly why it
		// needs a test of its own.
		renderFooter('unreachable');

		expect(screen.getByRole('contentinfo')).toHaveAttribute('data-api-status', 'unreachable');
	});

	it('still says it in words', () => {
		// The attribute is for machines; a visitor gets the same fact as a
		// sentence, and the technical value is interpolated into it rather than
		// translated.
		renderFooter('ok');

		expect(screen.getByText(/ok/i)).toBeInTheDocument();
	});
});
