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
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../../i18n';
import { foldersQueryOptions } from '../../server/folders';
import { tagsQueryOptions } from '../../server/tags';
import type { Me } from '../_authed';
import { Route } from './teams.$teamSlug.links.$linkId';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is a type this
   file does not own: TanStack Router's own route and context option shapes, and the generated
   `@kurze-url/api-client` `Link` type, whose nested arrays are mutable. */

const TEAM_ID = 'team-a';

const LINK: Link = {
	analytics_enabled: true,
	created_at: '2026-01-01T00:00:00.000Z',
	created_by: 'user-a',
	destination_url: 'https://example.org/sommerfest',
	domain_id: 'domain-a',
	expires_at: null,
	folder_id: 'folder-a',
	has_password: true,
	hostname: 'kurze.url',
	id: 'link-a',
	redirect_type: 302,
	short_url: 'https://kurze.url/sommerfest',
	slug: 'sommerfest',
	state: 'active',
	tags: [{ id: 'tag-a', name: 'Jugend', team_id: TEAM_ID }],
	team_id: TEAM_ID,
	updated_at: '2026-01-01T00:00:00.000Z',
};

/**
 * Renders the page's own route component, the one `routeTree.gen.ts` mounts at
 * `/teams/$teamSlug/links/$linkId`, so the role checks are exercised where
 * they live rather than through copies of them. The route hangs under a
 * pathless `_authed` layout as the generated tree does, and its own
 * `beforeLoad` runs for real; only the loader is replaced, because the real
 * one calls a server function, which cannot run under Vitest. The folder and
 * tag queries are seeded and never stale, so those make no request either.
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
	queryClient.setQueryData(foldersQueryOptions(TEAM_ID).queryKey, {
		items: [
			{ created_at: '2026-01-01T00:00:00Z', id: 'folder-a', name: 'Sommerfest', team_id: TEAM_ID },
		],
		page: 1,
		per_page: 100,
		total_count: 1,
	});
	queryClient.setQueryData(tagsQueryOptions(TEAM_ID).queryKey, [
		{ id: 'tag-a', name: 'Jugend', team_id: TEAM_ID },
	]);

	const rootRoute = createRootRouteWithContext<{ me: Me; queryClient: QueryClient }>()({
		component: Outlet,
	});
	const authedRoute = createRoute({ getParentRoute: () => rootRoute, id: '_authed' });
	// What `routeTree.gen.ts` does with `Route.update(...)`, which is
	// `Object.assign(Route.options, ...)` without its parent-route typing: it
	// hangs the real route under this test's own parent.
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

describe('the link page', () => {
	// The API refuses every write below editor with a 403, so a viewer reads the
	// link here and is offered nothing to change. What stays is what a viewer
	// can use: the QR code with its download, and the statistics.
	describe('for a viewer', () => {
		// The row link on the list reads "Details" below editor; the page it opens
		// must not then announce itself as an edit page above disabled fields.
		it('is headed "Details", not "Edit"', async () => {
			renderPage('viewer');

			await expect(
				screen.findByRole('heading', { level: 1, name: 'Details' }),
			).resolves.toBeInTheDocument();
			expect(screen.queryByRole('heading', { level: 1, name: 'Edit' })).not.toBeInTheDocument();
		});

		it('shows the link read-only', async () => {
			renderPage('viewer');

			const destination = await screen.findByLabelText('Destination URL');
			expect(destination).toBeDisabled();
			expect(destination).toHaveValue('https://example.org/sommerfest');
			expect(screen.getByLabelText('Short path')).toBeDisabled();
			expect(screen.getByLabelText('Folder')).toBeDisabled();
		});

		it('shows the tags but leaves the picker off, and has no way to save', async () => {
			renderPage('viewer');

			await expect(screen.findByText('Jugend')).resolves.toBeVisible();
			expect(screen.getByRole('combobox', { name: 'Tags' })).toBeDisabled();
			expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
		});

		it('reports the password status without controls to change it', async () => {
			renderPage('viewer');

			await expect(
				screen.findByText('This link is protected by a password.'),
			).resolves.toBeInTheDocument();
			expect(screen.queryByRole('button', { name: 'Change password' })).not.toBeInTheDocument();
			expect(screen.queryByRole('button', { name: 'Remove protection' })).not.toBeInTheDocument();
			expect(screen.queryByLabelText('Password')).not.toBeInTheDocument();
		});

		it('offers no way to delete the link, and no heading announcing one', async () => {
			renderPage('viewer');

			await screen.findByLabelText('Destination URL');
			expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
			expect(screen.queryByRole('heading', { name: 'Delete link' })).not.toBeInTheDocument();
		});

		it('keeps the QR code download and the link to the statistics', async () => {
			renderPage('viewer');

			await expect(screen.findByRole('button', { name: 'Download' })).resolves.toBeInTheDocument();
			expect(screen.getByRole('link', { name: 'Statistics' })).toHaveAttribute(
				'href',
				'/teams/verein-a/links/link-a/stats',
			);
		});
	});

	describe('for an editor', () => {
		it('is headed "Edit", not "Details"', async () => {
			renderPage('editor');

			await expect(
				screen.findByRole('heading', { level: 1, name: 'Edit' }),
			).resolves.toBeInTheDocument();
			expect(screen.queryByRole('heading', { level: 1, name: 'Details' })).not.toBeInTheDocument();
		});

		it('shows the form with its Save button', async () => {
			renderPage('editor');

			await expect(screen.findByLabelText('Destination URL')).resolves.toBeEnabled();
			expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument();
		});

		it('shows the password controls and the delete control', async () => {
			renderPage('editor');

			await expect(
				screen.findByRole('button', { name: 'Change password' }),
			).resolves.toBeInTheDocument();
			expect(screen.getByRole('button', { name: 'Remove protection' })).toBeInTheDocument();
			expect(screen.getByRole('heading', { name: 'Delete link' })).toBeInTheDocument();
			expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
		});
	});
});
