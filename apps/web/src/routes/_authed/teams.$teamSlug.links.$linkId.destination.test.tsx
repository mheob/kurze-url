import type { Link } from '@kurze-url/api-client';
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
import { foldersQueryOptions } from '../../server/folders';
import type * as LinksModule from '../../server/links';
import { tagsQueryOptions } from '../../server/tags';
import type { Me } from '../_authed';
import { Route } from './teams.$teamSlug.links.$linkId';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   file does not own: TanStack Router's own route and context option shapes, and the generated
   `@kurze-url/api-client` `Link` type, whose nested arrays are mutable. */

/**
 * `updateLinkFn` is a server function, unreachable under Vitest ("No Start
 * context found"), so it is replaced outright; everything else the module
 * exports stays real, the same partial mock `use-create-tag.test.tsx` uses.
 */
const mocks = vi.hoisted(() => ({
	updateLinkFn: vi.fn<(input: unknown) => Promise<never>>(),
}));

vi.mock('../../server/links', async (importOriginal) => ({
	...(await importOriginal<typeof LinksModule>()),
	updateLinkFn: mocks.updateLinkFn,
}));

const TEAM_ID = 'team-a';

const LINK: Link = {
	analytics_enabled: true,
	created_at: '2026-01-01T00:00:00.000Z',
	created_by: 'user-a',
	destination_url: 'https://example.org/sommerfest',
	domain_id: 'domain-a',
	expires_at: null,
	folder_id: 'folder-a',
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

/**
 * Renders the edit page's own route component for an editor, the same way
 * `teams.$teamSlug.links.$linkId.role.test.tsx` does: the real route under a
 * pathless `_authed` layout with its own `beforeLoad`, and only the loader
 * replaced, because the real one calls a server function.
 *
 * Named `....$linkId.destination.test.tsx` rather than sharing the role
 * test's file, because the `vi.mock` above replaces `updateLinkFn` for every
 * test in the file that declares it.
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
	queryClient.setQueryData(foldersQueryOptions(TEAM_ID).queryKey, {
		items: [
			{ created_at: '2026-01-01T00:00:00Z', id: 'folder-a', name: 'Sommerfest', team_id: TEAM_ID },
		],
		page: 1,
		per_page: 100,
		total_count: 1,
	});
	queryClient.setQueryData(tagsQueryOptions(TEAM_ID).queryKey, []);

	const rootRoute = createRootRouteWithContext<{ me: Me; queryClient: QueryClient }>()({
		component: Outlet,
	});
	const authedRoute = createRoute({ getParentRoute: () => rootRoute, id: '_authed' });
	Object.assign(Route.options, {
		getParentRoute: () => authedRoute,
		loader: () => LINK,
		path: '/teams/$teamSlug/links/$linkId',
	});
	const router = createRouter({
		context: { me, queryClient },
		history: createMemoryHistory({ initialEntries: ['/teams/verein-a/links/link-a'] }),
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

describe('the link page, when the API refuses a new destination', () => {
	// The API's typed 422 (`destinationRejected`, apps/api/internal/api/links.go):
	// the English sentence in `detail`, the reason as a token at the field.
	it('says why at the destination field, in the reader’s language, and raises no banner', async () => {
		mocks.updateLinkFn.mockRejectedValue({
			detail: 'destination: private and local addresses are not allowed: 10.0.0.1',
			errors: [
				{
					location: 'body.destination_url',
					message: 'destination: private and local addresses are not allowed: 10.0.0.1',
					value: 'private_address',
				},
			],
			status: 422,
			title: 'Unprocessable Entity',
		});
		renderPage();

		const destination = await screen.findByLabelText('Destination URL');
		await userEvent.clear(destination);
		await userEvent.type(destination, 'https://10.0.0.1/');
		await userEvent.click(screen.getByRole('button', { name: 'Save' }));

		await expect(
			screen.findByText('Private and local network addresses are not allowed.'),
		).resolves.toBeInTheDocument();
		expect(destination).toHaveAccessibleDescription(
			'Private and local network addresses are not allowed.',
		);
		expect(destination).toHaveAttribute('aria-invalid', 'true');
		expect(screen.queryByText('Something went wrong. Please try again.')).not.toBeInTheDocument();
		expect(screen.queryByText(/private and local addresses/u)).not.toBeInTheDocument();
	});
});
