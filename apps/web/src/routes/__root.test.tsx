import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	Outlet,
	RouterProvider,
} from '@tanstack/react-router';
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

// `RootDocument` renders `<TanStackDevtools>` unconditionally, and mounting
// the real devtools panel inside jsdom throws on unmount ("Devtools is not
// mounted") once RTL's automatic per-test cleanup runs — a jsdom/devtools
// integration gap, not something this suite is testing. Stubbed to a no-op
// for exactly the tests below that render the whole document shell.
vi.mock('@tanstack/react-devtools', () => ({ TanStackDevtools: () => null }));

// oxlint-disable-next-line node/no-top-level-await -- `vi.mock` above is hoisted; importing the subject module only after it, at module scope, is Vitest's own documented way to get a mocked dependency into an ESM import — the same pattern every other `*.test.ts(x)` in this app that mocks an import uses.
const { RootDocument, RootErrorPage } = await import('./__root');

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

/**
 * `RootDocument` is not itself a route component — it reads `Route.useLoaderData()`
 * (`Route` being the real root route's own export), which throws outside a
 * router that actually matched that route. There is no way to call
 * `RootDocument` directly and no way to substitute a fake `Route` for the one
 * it closes over, so this builds a real, minimal router instead: a synthetic
 * root route supplies the loader data and reuses `RootDocument` as its own
 * `shellComponent`, exactly like the real `Route` does. Root routes always
 * carry the fixed id `__root__`, which is all `Route.useLoaderData()` keys
 * off — so the real `RootDocument`, imported unchanged, resolves this
 * synthetic route's loader data as if it were the genuine one.
 *
 * React 19 treats `<html>`/`<head>`/`<body>` as singletons: rendering them
 * anywhere patches the real `document.documentElement`/`document.head`/
 * `document.body` rather than nesting a second document inside RTL's
 * container `<div>` — confirmed empirically (see task-2-report.md) before
 * relying on it here. That is what makes asserting on
 * `document.documentElement` below meaningful rather than a check against a
 * detached tree the browser never uses.
 *
 * @returns The render result.
 */
function renderRootDocument(): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => <Outlet />,
		loader: () => ({ language: 'en' as const, theme: 'light' as const }),
		shellComponent: RootDocument,
	});
	const indexRoute = createRoute({
		component: () => <p>{'root document marker'}</p>,
		getParentRoute: () => rootRoute,
		path: '/',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([indexRoute]),
	});

	return render(<RouterProvider router={router} />);
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

describe('rootDocument', () => {
	it('renders the colour theme attribute on the document element', async () => {
		// Without this attribute every colour token is undefined and the whole
		// app renders unstyled — a failure that looks like a CSS build problem
		// rather than a missing attribute, so it is pinned here.
		renderRootDocument();

		await expect(screen.findByText('root document marker')).resolves.toBeInTheDocument();
		expect(document.documentElement).toHaveAttribute('data-theme', 'indigo');
	});
});
