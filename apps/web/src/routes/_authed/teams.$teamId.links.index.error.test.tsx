import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	Outlet,
	RouterProvider,
	type AnyRouter,
} from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../../i18n';
import { LinksError } from './teams.$teamId.links.index';

/**
 * Named `....index.error.test.tsx`, not `....index.test.tsx` (which would
 * match `loadLinks`'s own test file save for the extension): co-locating a
 * `.test.ts` and a `.test.tsx` under the exact same basename made oxlint's
 * type-aware checker (`oxlint-tsgolint`) misresolve `@testing-library/jest-dom`'s
 * ambient `Assertion` augmentation for the `.tsx` file only — `toBeInTheDocument`
 * and `toHaveTextContent` came back as type errors that `tsc` itself never
 * raised, confirmed by reproducing it with two throwaway same-named files.
 * The distinct basename here sidesteps the collision rather than explaining it.
 *
 * `LinksError` renders `<Navigate>` for an unauthenticated failure, which
 * needs a router in context to resolve and commit `to` — the same reason
 * `team-switcher.test.tsx` builds a minimal route tree rather than rendering
 * its component bare. Unlike that test, this one actually navigates, so the
 * root route here renders `<Outlet />` (matching `__root.tsx`'s own shape):
 * without it, the router's own state still moves to `/login` internally,
 * but nothing ever mounts the `/login` route's component into the DOM,
 * since there is no outlet to mount it into. The `/login` route renders a
 * marker unique to it, so a test can tell "navigated to /login" apart from
 * "rendered something, somewhere" just from what ends up on screen.
 */
function renderWith(error: unknown): { readonly router: AnyRouter } {
	const rootRoute = createRootRoute({ component: () => <Outlet /> });
	const indexRoute = createRoute({
		getParentRoute: () => rootRoute,
		path: '/',
		component: () => <LinksError error={error} />,
	});
	const loginRoute = createRoute({
		getParentRoute: () => rootRoute,
		path: '/login',
		component: () => <p>{'login page marker'}</p>,
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

	return { router };
}

describe('LinksError', () => {
	/**
	 * The finding this fixes (Fix round 2): React Query's default
	 * `refetchOnWindowFocus` (`router.tsx` sets no `defaultOptions`) means a
	 * background refetch of the already-mounted list query can throw an
	 * unauthenticated failure to this boundary directly — a path
	 * `loadLinks`'s own redirect (covered in `teams.$teamId.links.index.test.ts`)
	 * never sees, since it isn't a loader run at all. Asserting only that
	 * *some* navigation fired would still pass if `<Navigate>` targeted the
	 * wrong route, or if the guard were deleted and something else entirely
	 * happened to also change the screen — this asserts the actual
	 * destination two ways: the marker unique to `/login`, and the router's
	 * own committed location.
	 */
	it('redirects to /login when the failure is unauthenticated', async () => {
		const { router } = renderWith({ status: 401 });

		expect(await screen.findByText('login page marker')).toBeInTheDocument();
		await waitFor(() => expect(router.state.location.pathname).toBe('/login'));
	});

	/**
	 * The list still has to fail loudly for a genuinely down API — an empty
	 * list is indistinguishable from a team with no links, which `LinksError`
	 * exists to avoid. A non-401 failure must keep rendering inline instead
	 * of also being redirected away.
	 */
	it('renders the error alert for a non-401 failure', async () => {
		renderWith({ status: 500 });

		expect(await screen.findByRole('alert')).toHaveTextContent(
			'Something went wrong. Please try again.',
		);
	});
});
