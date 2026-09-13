import type * as SentryModule from '@sentry/tanstackstart-react';
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
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { createI18n } from '../../i18n';

const sentryMocks = vi.hoisted(() => ({ captureException: vi.fn() }));

/**
 * `captureException` is mocked rather than `reportUnexpected`, so the real
 * `isReportable` filter still runs: this file is about what actually leaves
 * for Sentry, not about whether a function was called.
 */
vi.mock('@sentry/tanstackstart-react', async (importOriginal) => ({
	...(await importOriginal<typeof SentryModule>()),
	captureException: sentryMocks.captureException,
}));

// oxlint-disable-next-line node/no-top-level-await -- `vi.mock` above is hoisted; importing the subject modules only after it, at module scope, is Vitest's own documented way to get a mocked dependency into an ESM import — the same pattern every other `*.test.ts(x)` in this app that mocks an import uses.
const { LinksError } = await import('./teams.$teamSlug.links.index');
// oxlint-disable-next-line node/no-top-level-await -- same reason as the import above.
const { DomainsError } = await import('./teams.$teamSlug.domains');

/**
 * The finding (Fix round 3): `LinksError` and `DomainsError` are route-level
 * `errorComponent`s, and TanStack Router renders the *nearest* one — so a 500
 * from listing links or domains, the likeliest real failure in the
 * authenticated app, rendered here and never reached `RootErrorPage`, the one
 * place that was reporting. `__root.test.tsx` covers the root boundary; this
 * file covers the two that shadow it.
 *
 * Both components render `<Navigate>` for an unauthenticated failure, which
 * needs a router in context — the same minimal tree
 * `teams.$teamSlug.links.index.error.test.tsx` builds, and for the same reason.
 *
 * @param element - The error component under test, already given its `error` prop.
 */
function renderInRouter(element: Readonly<React.JSX.Element>): void {
	const rootRoute = createRootRoute({ component: () => <Outlet /> });
	const indexRoute = createRoute({
		component: () => element,
		getParentRoute: () => rootRoute,
		path: '/',
	});
	const loginRoute = createRoute({
		component: () => <p>{'login page marker'}</p>,
		getParentRoute: () => rootRoute,
		path: '/login',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([indexRoute, loginRoute]),
	});

	render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe.each<Readonly<{ Component: typeof DomainsError | typeof LinksError; name: string }>>([
	{ Component: LinksError, name: 'LinksError' },
	{ Component: DomainsError, name: 'DomainsError' },
])('$name', ({ Component }) => {
	beforeEach(() => {
		sentryMocks.captureException.mockClear();
	});

	it('reports an unexpected failure', async () => {
		const error = { status: 500 };

		renderInRouter(<Component error={error} />);

		await expect(screen.findByRole('alert')).resolves.toBeInTheDocument();
		expect(sentryMocks.captureException).toHaveBeenCalledWith(error);
	});

	/**
	 * A 403 is a failure this app deliberately renders as UI. Reporting it
	 * would spend one of the month's 5,000 events on something nobody needs
	 * to be told about — and, worse, would drown the failures that do.
	 */
	it('does not report a failure the app renders on purpose', async () => {
		renderInRouter(<Component error={{ status: 403 }} />);

		await expect(screen.findByRole('alert')).resolves.toBeInTheDocument();
		expect(sentryMocks.captureException).not.toHaveBeenCalled();
	});
});
