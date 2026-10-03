import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
	createMemoryHistory,
	createRootRouteWithContext,
	createRoute,
	createRouter,
	Outlet,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../../i18n';
import { domainsQueryOptions } from '../../server/domains';
import { foldersQueryOptions } from '../../server/folders';
import { tagsQueryOptions } from '../../server/tags';
import type { Me } from '../_authed';
import { Route } from './teams.$teamSlug.links.new';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   file does not own: TanStack Router's own route and context option shapes, and the generated
   `@kurze-url/api-client` page types the seeded queries hold. */

const TEAM_ID = 'team-a';

/**
 * Renders the page's own route component, the one `routeTree.gen.ts` mounts at
 * `/teams/$teamSlug/links/new`, so the role check is exercised where it lives
 * rather than through a copy of it. The route is hung under a pathless
 * `_authed` layout exactly as the generated tree does, its own `beforeLoad`
 * and `loader` run for real, and the three queries they read are seeded and
 * never stale, so no request is ever made.
 *
 * @param role - The signed-in member's role on the team.
 * @returns Testing Library's render result.
 */
function renderPage(role: string): ReturnType<typeof render> {
	const me: Me = {
		email: 'kasse@verein-a.example',
		is_maintainer: false,
		memberships: [{ name: 'Verein A', role, slug: 'verein-a', team_id: TEAM_ID }],
		user_id: 'user-a',
	};
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
	});
	const emptyPage = { items: [], page: 1, per_page: 100, total_count: 0 };
	queryClient.setQueryData(domainsQueryOptions(TEAM_ID).queryKey, emptyPage);
	queryClient.setQueryData(foldersQueryOptions(TEAM_ID).queryKey, emptyPage);
	queryClient.setQueryData(tagsQueryOptions(TEAM_ID).queryKey, []);

	const rootRoute = createRootRouteWithContext<{ me: Me; queryClient: QueryClient }>()({
		component: Outlet,
	});
	const authedRoute = createRoute({ getParentRoute: () => rootRoute, id: '_authed' });
	// What `routeTree.gen.ts` does with `Route.update(...)`, which is
	// `Object.assign(Route.options, ...)` without its parent-route typing: it
	// hangs the real route under this test's own parent.
	Object.assign(Route.options, {
		getParentRoute: () => authedRoute,
		path: '/teams/$teamSlug/links/new',
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
	});
	const router = createRouter({
		context: { me, queryClient },
		history: createMemoryHistory({ initialEntries: ['/teams/verein-a/links/new'] }),
		routeTree: rootRoute.addChildren([authedRoute.addChildren([Route]), linksRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<QueryClientProvider client={queryClient}>
				<RouterProvider router={router} />
			</QueryClientProvider>
		</I18nextProvider>,
	);
}

describe('the create-link route', () => {
	// The API refuses `POST /v1/teams/{id}/links` below editor, so a viewer who
	// reaches this address (a bookmark, a link from a colleague) is told so
	// instead of being handed a form that can only fail once it is filled in.
	it('tells a viewer why there is no form, and offers the way back', async () => {
		renderPage('viewer');

		await expect(
			screen.findByRole('heading', { level: 1, name: 'Creating links needs editing rights' }),
		).resolves.toBeInTheDocument();
		expect(
			screen.getByText(
				'Your role in this team lets you view links, not create them. Ask an admin or owner if you need to create links.',
			),
		).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Back to links' })).toHaveAttribute(
			'href',
			'/teams/verein-a/links',
		);
		expect(screen.queryByLabelText('Destination URL')).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
	});

	it('shows an editor the form and no notice', async () => {
		renderPage('editor');

		await expect(screen.findByLabelText('Destination URL')).resolves.toBeEnabled();
		expect(screen.getByRole('heading', { level: 1, name: 'Create link' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument();
		expect(
			screen.queryByRole('heading', { name: 'Creating links needs editing rights' }),
		).not.toBeInTheDocument();
	});
});
