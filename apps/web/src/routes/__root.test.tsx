import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type * as ObservabilityModule from '../lib/observability';

const mocks = vi.hoisted(() => ({ reportUnexpected: vi.fn() }));

vi.mock('../lib/observability', async (importOriginal) => ({
	...(await importOriginal<typeof ObservabilityModule>()),
	reportUnexpected: mocks.reportUnexpected,
}));

// oxlint-disable-next-line node/no-top-level-await -- `vi.mock` above is hoisted; importing the subject module only after it, at module scope, is Vitest's own documented way to get a mocked dependency into an ESM import — the same pattern every other `*.test.ts(x)` in this app that mocks an import uses.
const { RootErrorPage } = await import('./__root');

/**
 * `RootErrorPage` reads its copy through `useTranslation`, which needs an
 * `I18nextProvider` in scope — without one, `t()` has no instance to draw
 * from and silently renders the raw key instead of either locale's text.
 * `login.test.tsx` settled on the same `createI18n` + `I18nextProvider`
 * wrapper for the same reason.
 *
 * @param error - The value passed to `RootErrorPage`'s `error` prop.
 * @returns The render result.
 */
function renderRootErrorPage(error: unknown): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			{/* `reset` is the router's retry callback, required by
			    `ErrorComponentProps` and unused by this page — it renders one
			    generic sentence and offers nothing to retry. */}
			<RootErrorPage error={error} reset={vi.fn((): void => undefined)} />
		</I18nextProvider>,
	);
}

describe('rootErrorPage', () => {
	it('reports the failure it renders', () => {
		const error = new Error('boom');

		renderRootErrorPage(error);

		expect(mocks.reportUnexpected).toHaveBeenCalledWith(error);
	});

	/**
	 * Reported and also *shown*. An error component that reports silently
	 * and renders nothing leaves the visitor on a blank page, which is how
	 * the "Something went wrong" episode looked from the outside. Asserting
	 * on the actual translated copy (rather than just the role) is what
	 * catches a typo in the key or a missing locale entry.
	 */
	it('tells the visitor something went wrong', () => {
		renderRootErrorPage(new Error('boom'));

		expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong. Please try again.');
	});

	/**
	 * `role="alert"` sits on the wrapping `<div>`, not the `<h1>` itself —
	 * an explicit `role` on the heading would replace its implicit "heading"
	 * semantics with "alert", leaving a heading-navigating assistive
	 * technology user with nothing to find on this page.
	 */
	it('keeps the message reachable as a heading', () => {
		renderRootErrorPage(new Error('boom'));

		expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
			'Something went wrong. Please try again.',
		);
	});
});
