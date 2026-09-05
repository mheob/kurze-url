import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import { ShortUrlNotice } from './short-url-notice';

/**
 * A component reaching through `useTranslation` needs an `I18nextProvider` in
 * its tree — see `language-switcher.test.tsx`'s own note on the same point.
 */
function renderWith(hostname: string): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<ShortUrlNotice hostname={hostname} />
		</I18nextProvider>,
	);
}

describe('ShortUrlNotice', () => {
	it('warns when the shared domain cannot resolve', () => {
		// SHARED_DOMAIN_HOSTNAME is short.invalid until a real domain is
		// registered. Showing a copy button for a URL that 404s, with no
		// explanation, is the confusion the .invalid placeholder exists to
		// prevent.
		renderWith('short.invalid');
		expect(screen.getByRole('note')).toBeInTheDocument();
	});

	it('renders nothing once a real domain is configured', () => {
		// The condition is the hostname, not a feature flag: the notice must
		// disappear on its own the day the domain changes, with no code edit
		// and nothing to remember to switch off.
		const { container } = renderWith('kurze.url');
		expect(container).toBeEmptyDOMElement();
	});
});
