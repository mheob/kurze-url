import type { Domain, PageDomain } from '@kurze-url/api-client';
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
import type { Me } from '../_authed';
import { Route } from './teams.$teamSlug.domains';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   file does not own: TanStack Router's own route and context option shapes, and the generated
   `@kurze-url/api-client` `Domain`/`PageDomain` types, whose properties are mutable. */

const TEAM_ID = 'team-a';

const PENDING: Domain = {
	hostname: 'links.verein.test',
	id: 'domain-1',
	records: {
		cname: { name: 'links.verein.test', value: 'cname.vercel-dns.com' },
		txt: { name: '_kurze-url-challenge.links.verein.test', value: 'tok-a' },
	},
	team_id: TEAM_ID,
	verification_status: 'pending',
	verification_token: 'tok-a',
	verified_at: null,
};

const PAGE: PageDomain = { items: [PENDING], page: 1, per_page: 100, total_count: 1 };

/**
 * Renders the page's own route component, the one `routeTree.gen.ts` mounts at
 * `/teams/$teamSlug/domains`, so the role check is exercised where it lives
 * rather than through a copy of it. The route hangs under a pathless
 * `_authed` layout as the generated tree does, its own `beforeLoad` and
 * `loader` run for real, and the one query they read is seeded and never
 * stale, so no request is ever made.
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
	queryClient.setQueryData(domainsQueryOptions(TEAM_ID).queryKey, PAGE);

	const rootRoute = createRootRouteWithContext<{ me: Me; queryClient: QueryClient }>()({
		component: Outlet,
	});
	const authedRoute = createRoute({ getParentRoute: () => rootRoute, id: '_authed' });
	// What `routeTree.gen.ts` does with `Route.update(...)`, which is
	// `Object.assign(Route.options, ...)` without its parent-route typing: it
	// hangs the real route under this test's own parent.
	Object.assign(Route.options, {
		getParentRoute: () => authedRoute,
		path: '/teams/$teamSlug/domains',
	});
	const router = createRouter({
		context: { me, queryClient },
		history: createMemoryHistory({ initialEntries: ['/teams/verein-a/domains'] }),
		routeTree: rootRoute.addChildren([authedRoute.addChildren([Route])]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<QueryClientProvider client={queryClient}>
				<RouterProvider router={router} />
			</QueryClientProvider>
		</I18nextProvider>,
	);
}

describe('the domains page', () => {
	// The API refuses claim, verify and delete below admin with a 403. What every
	// member can still use is the list and, for a pending domain, the DNS records.
	describe.each(['viewer', 'editor'])('for %s', (role) => {
		it('lists the domains and their DNS records, with no claim form', async () => {
			renderPage(role);

			await expect(
				screen.findByRole('heading', { level: 1, name: 'Your domains' }),
			).resolves.toBeInTheDocument();
			expect(screen.getByText('_kurze-url-challenge.links.verein.test')).toBeInTheDocument();
			expect(screen.queryByLabelText('Hostname')).not.toBeInTheDocument();
			expect(screen.queryByRole('button', { name: 'Add domain' })).not.toBeInTheDocument();
		});

		it('offers neither Check now nor a delete button', async () => {
			renderPage(role);

			await screen.findByRole('heading', { level: 1, name: 'Your domains' });
			expect(screen.queryByRole('button', { name: 'Check now' })).not.toBeInTheDocument();
			expect(screen.queryByRole('button', { name: /^Delete /u })).not.toBeInTheDocument();
		});
	});

	describe.each(['admin', 'owner'])('for %s', (role) => {
		it('offers the claim form, Check now and a delete button', async () => {
			renderPage(role);

			await expect(screen.findByLabelText('Hostname')).resolves.toBeEnabled();
			expect(screen.getByRole('button', { name: 'Add domain' })).toBeInTheDocument();
			expect(screen.getByRole('button', { name: 'Check now' })).toBeInTheDocument();
			expect(screen.getByRole('button', { name: 'Delete links.verein.test' })).toBeInTheDocument();
		});
	});
});
