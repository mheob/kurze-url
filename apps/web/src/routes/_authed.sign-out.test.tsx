import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	Outlet,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';

import { createI18n } from '../i18n';
import type { Me } from './_authed';

/**
 * The server function, replaced by a fake typed with only what the layout passes it. The real one
 * reaches `getRequest()`, which has no Start context under Vitest; what it hands Supabase is
 * `server/auth.test.ts`'s to test.
 */
const mocks = vi.hoisted(() => ({
	signOut:
		vi.fn<
			(input: Readonly<{ data: Readonly<{ scope: 'global' | 'local' }> }>) => Promise<undefined>
		>(),
}));

vi.mock('../server/auth', () => ({ signOut: mocks.signOut }));

// oxlint-disable-next-line node/no-top-level-await -- `vi.mock` above is hoisted; importing the subject module only after it, at module scope, is Vitest's own documented way to get a mocked dependency into an ESM import, as `oauth.consent.test.tsx` does.
const { Route } = await import('./_authed');

const ME: Me = {
	email: 'sabine@sv-gruenwald.example',
	is_maintainer: false,
	memberships: [{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' }],
	user_id: 'user-a',
};

/**
 * Mounts the layout the real `/_authed` route declares, so what is under test is its own
 * `AuthedLayout`, the sign-out mutation inside it and the shell it renders, none of them copied.
 * The route is hung under this test's own root the way `routeTree.gen.ts` hangs it under the real
 * one (`Object.assign(Route.options, ...)`, the trick `oauth.consent.test.tsx` uses), with one
 * difference: `beforeLoad` is replaced by one that answers a fixed `me`, because the real one
 * calls the `fetchMe` server function.
 *
 * @returns Testing Library's render result.
 */
function renderLayout(): ReturnType<typeof render> {
	const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	// `usePreferences` reads the root route's loader data, so the test root has to supply it.
	const rootRoute = createRootRoute({
		component: Outlet,
		loader: () => ({ language: 'en' as const, theme: 'light' as const }),
	});
	Object.assign(Route.options, {
		beforeLoad: () => ({ me: ME }),
		getParentRoute: () => rootRoute,
		id: '_authed',
	});
	const homeRoute = createRoute({
		component: () => null,
		getParentRoute: () => Route,
		path: '/',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([Route.addChildren([homeRoute])]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<QueryClientProvider client={queryClient}>
				<RouterProvider router={router} />
			</QueryClientProvider>
		</I18nextProvider>,
	);
}

describe('signing out of the authenticated layout', () => {
	beforeEach(() => {
		mocks.signOut.mockReset();
		mocks.signOut.mockResolvedValue(undefined);
	});

	// The default is the point of the whole change: a plain sign-out used to end every session the
	// person had, the CLI's included, and now ends only this browser's.
	it('signs out locally when the box is left alone', async () => {
		renderLayout();

		await userEvent.click(await screen.findByRole('button', { name: 'Sign out' }));

		expect(mocks.signOut).toHaveBeenCalledExactlyOnceWith({ data: { scope: 'local' } });
	});

	it('signs out globally when the box is checked', async () => {
		renderLayout();

		await userEvent.click(await screen.findByRole('checkbox', { name: 'Sign out everywhere' }));
		await userEvent.click(screen.getByRole('button', { name: 'Sign out' }));

		expect(mocks.signOut).toHaveBeenCalledExactlyOnceWith({ data: { scope: 'global' } });
	});

	/**
	 * A global sign-out Supabase refused rejects (`signOutFor`), and the layout's existing error
	 * state is what tells the person: they stay signed in, with the box still ticked, and see the
	 * alert instead of being sent away as if it had worked.
	 */
	it('shows the error, and stays put, when signing out everywhere fails', async () => {
		mocks.signOut.mockRejectedValue(new Error('global sign-out failed'));
		renderLayout();

		await userEvent.click(await screen.findByRole('checkbox', { name: 'Sign out everywhere' }));
		await userEvent.click(screen.getByRole('button', { name: 'Sign out' }));

		await expect(screen.findByRole('alert')).resolves.toHaveTextContent(
			'Something went wrong. Please try again.',
		);
		expect(screen.getByRole('checkbox', { name: 'Sign out everywhere' })).toBeChecked();
	});
});
