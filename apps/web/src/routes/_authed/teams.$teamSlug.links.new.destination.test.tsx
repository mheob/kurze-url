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
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vite-plus/test';

import { createI18n } from '../../i18n';
import { domainsQueryOptions } from '../../server/domains';
import { foldersQueryOptions } from '../../server/folders';
import type * as LinksModule from '../../server/links';
import { tagsQueryOptions } from '../../server/tags';
import type { Me } from '../_authed';
import { Route } from './teams.$teamSlug.links.new';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   file does not own: TanStack Router's own route and context option shapes, and the generated
   `@kurze-url/api-client` page types the seeded queries hold. */

/**
 * `createLinkFn` is a server function, unreachable under Vitest ("No Start
 * context found"), so it is replaced outright; everything else the module
 * exports stays real, the same partial mock `use-create-tag.test.tsx` uses.
 */
const mocks = vi.hoisted(() => ({
	createLinkFn: vi.fn<(input: unknown) => Promise<never>>(),
}));

vi.mock('../../server/links', async (importOriginal) => ({
	...(await importOriginal<typeof LinksModule>()),
	createLinkFn: mocks.createLinkFn,
}));

const TEAM_ID = 'team-a';

/**
 * Renders the create page's own route component for an editor, the same way
 * `teams.$teamSlug.links.new.role.test.tsx` does: the real route hung under a
 * pathless `_authed` layout, its `beforeLoad` and `loader` running for real
 * against seeded, never-stale queries.
 *
 * Named `....new.destination.test.tsx` rather than sharing the role test's
 * file, because the `vi.mock` above replaces `createLinkFn` for every test in
 * the file that declares it.
 *
 * @returns Testing Library's render result.
 */
function renderPage(): ReturnType<typeof render> {
	const me: Me = {
		email: 'kasse@verein-a.example',
		is_maintainer: false,
		memberships: [{ name: 'Verein A', role: 'editor', slug: 'verein-a', team_id: TEAM_ID }],
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

describe('the create-link route, when the API refuses the destination', () => {
	// The API's typed 422 (`destinationRejected`, apps/api/internal/api/links.go):
	// the English sentence in `detail`, the reason as a token at the field.
	it('says why at the destination field, in the reader’s language, and raises no banner', async () => {
		mocks.createLinkFn.mockRejectedValue({
			detail: 'destination: only https:// destinations are allowed: got "http"',
			errors: [
				{
					location: 'body.destination_url',
					message: 'destination: only https:// destinations are allowed: got "http"',
					value: 'scheme',
				},
			],
			status: 422,
			title: 'Unprocessable Entity',
		});
		renderPage();

		const destination = await screen.findByLabelText('Destination URL');
		await userEvent.type(destination, 'http://example.org');
		await userEvent.click(screen.getByRole('button', { name: 'Save' }));

		await expect(
			screen.findByText('Only https:// addresses are allowed.'),
		).resolves.toBeInTheDocument();
		expect(destination).toHaveAccessibleDescription('Only https:// addresses are allowed.');
		expect(destination).toHaveAttribute('aria-invalid', 'true');
		expect(screen.queryByText('Something went wrong. Please try again.')).not.toBeInTheDocument();
		expect(screen.queryByText(/only https:\/\/ destinations/u)).not.toBeInTheDocument();
	});
});
