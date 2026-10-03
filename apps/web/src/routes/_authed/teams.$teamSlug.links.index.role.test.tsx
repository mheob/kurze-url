import type { Link, PageLink } from '@kurze-url/api-client';
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
import { foldersQueryOptions } from '../../server/folders';
import { linksQueryOptions } from '../../server/links';
import { tagsQueryOptions } from '../../server/tags';
import type { Me } from '../_authed';
import { Route } from './teams.$teamSlug.links.index';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   file does not own: TanStack Router's own route and context option shapes, and the generated
   `@kurze-url/api-client` `Link`/`PageLink` types, whose nested arrays are mutable. */

const TEAM_ID = 'team-a';

const LINK: Link = {
	analytics_enabled: true,
	created_at: '2026-01-01T00:00:00.000Z',
	created_by: 'user-a',
	destination_url: 'https://example.org/sommerfest',
	domain_id: 'domain-a',
	expires_at: null,
	has_password: false,
	hostname: 'kurze.url',
	id: 'link-a',
	redirect_type: 302,
	short_url: 'https://kurze.url/sommerfest',
	slug: 'sommerfest',
	state: 'active',
	tags: [],
	team_id: TEAM_ID,
	updated_at: '2026-01-01T00:00:00.000Z',
};

const PAGE: PageLink = { items: [LINK], page: 1, per_page: 20, total_count: 1 };

/**
 * Renders the list's own route component, the one `routeTree.gen.ts` mounts at
 * `/teams/$teamSlug/links/`, so the role check is exercised where it lives
 * rather than through a copy of it. The route hangs under a pathless
 * `_authed` layout as the generated tree does, and its own `beforeLoad`
 * (which reads the role out of `me.memberships`) and `loader` run for real.
 * The three queries they read are seeded and never stale, so no request is
 * made. The create and link pages the list points at are stubs, only there so
 * its links resolve.
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
	queryClient.setQueryData(
		linksQueryOptions(TEAM_ID, 1, { folder: { kind: 'all' } }).queryKey,
		PAGE,
	);
	queryClient.setQueryData(foldersQueryOptions(TEAM_ID).queryKey, {
		items: [],
		page: 1,
		per_page: 100,
		total_count: 0,
	});
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
		path: '/teams/$teamSlug/links/',
	});
	const newLinkRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links/new',
	});
	const linkRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links/$linkId',
	});
	const router = createRouter({
		context: { me, queryClient },
		history: createMemoryHistory({ initialEntries: ['/teams/verein-a/links'] }),
		routeTree: rootRoute.addChildren([authedRoute.addChildren([Route]), newLinkRoute, linkRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<QueryClientProvider client={queryClient}>
				<RouterProvider router={router} />
			</QueryClientProvider>
		</I18nextProvider>,
	);
}

describe('the link list route', () => {
	// The API refuses creating links below editor, and the page behind a row
	// link is read-only there, so a viewer is offered neither a way to create
	// nor a promise of an edit.
	describe('for a viewer', () => {
		it('offers no way to create a link', async () => {
			renderPage('viewer');

			await expect(
				screen.findByRole('link', { name: 'https://kurze.url/sommerfest' }),
			).resolves.toBeInTheDocument();
			expect(screen.queryByRole('link', { name: 'Create link' })).not.toBeInTheDocument();
		});

		it('words the row link "Details", addressed at the link page', async () => {
			renderPage('viewer');

			await expect(screen.findByRole('link', { name: 'Details' })).resolves.toHaveAttribute(
				'href',
				'/teams/verein-a/links/link-a',
			);
			expect(screen.queryByRole('link', { name: 'Edit' })).not.toBeInTheDocument();
		});
	});

	describe('for an editor', () => {
		it('offers to create a link and words the row link "Edit"', async () => {
			renderPage('editor');

			await expect(screen.findByRole('link', { name: 'Create link' })).resolves.toHaveAttribute(
				'href',
				'/teams/verein-a/links/new',
			);
			expect(screen.getByRole('link', { name: 'Edit' })).toHaveAttribute(
				'href',
				'/teams/verein-a/links/link-a',
			);
			expect(screen.queryByRole('link', { name: 'Details' })).not.toBeInTheDocument();
		});
	});
});
