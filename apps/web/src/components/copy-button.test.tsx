import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import { CopyButton } from './copy-button';

function renderWith(value: string): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<CopyButton value={value} />
		</I18nextProvider>,
	);
}

describe('CopyButton', () => {
	it('copies the given value to the clipboard', async () => {
		// jsdom itself has no Clipboard implementation at all — `userEvent.setup()`
		// is what installs a working in-memory stub on `navigator.clipboard`
		// (`@testing-library/user-event`'s own `Clipboard.js`, unconditionally, on
		// every `setup()` call); reading it back through `readText()` is what
		// this test can actually observe, rather than a spy this library's own
		// stub would bypass.
		const user = userEvent.setup();
		renderWith('https://kurze.url/abc123');

		await user.click(screen.getByRole('button', { name: 'Copy' }));

		await expect(navigator.clipboard.readText()).resolves.toBe('https://kurze.url/abc123');
	});

	/**
	 * A colour change is invisible to a screen reader. This is the assertion
	 * that fails if the confirmation is only ever shown visually — see the
	 * falsification note in the task report.
	 */
	it('announces the confirmation through an aria-live region', async () => {
		const user = userEvent.setup();
		renderWith('https://kurze.url/abc123');

		const liveRegion = document.querySelector('[aria-live="polite"]');
		expect(liveRegion).not.toBeNull();
		expect(liveRegion).toHaveTextContent('');

		await user.click(screen.getByRole('button', { name: 'Copy' }));

		expect(await screen.findByText('Copied')).toBe(liveRegion);
	});
});
