import type { Link as ApiLink, PageLink } from '@kurze-url/api-client';
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import { LinkList } from './link-list';

function link(overrides: Partial<ApiLink> = {}): ApiLink {
	return {
		analytics_enabled: true,
		created_at: '2026-01-01T00:00:00Z',
		created_by: 'user-1',
		destination_url: 'https://example.org/',
		domain_id: 'domain-1',
		expires_at: null,
		folder_id: 'folder-1',
		has_password: false,
		hostname: 'short.invalid',
		id: 'link-1',
		redirect_type: 302,
		short_url: 'https://short.invalid/abc123',
		slug: 'abc123',
		state: 'active',
		tags: [],
		team_id: 'team-a',
		updated_at: '2026-01-01T00:00:00Z',
		...overrides,
	};
}

function pageOf(overrides: Partial<PageLink> = {}): PageLink {
	return { items: [], page: 1, per_page: 20, total_count: 0, ...overrides };
}

/**
 * `LinkList` renders TanStack Router `<Link>` elements for pagination, which
 * need a router in context — the same reasoning `team-switcher.test.tsx`
 * gives for its own minimal, test-only route tree. Also registers
 * `/teams/$teamId/links/new` (Finding 2's create-link entry point) and
 * `/teams/$teamId/links/$linkId` (Finding 2's edit-link entry point): `<Link>`
 * builds its `href` from the `to` path template regardless of whether this
 * router's own tree contains a matching route (verified — omitting either
 * here does not fail any test), but registering them keeps this fixture
 * honest about the routes actually existing, matching every other route
 * `<Link>` here targets.
 */
function renderWith(data: PageLink, page = 1): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => <LinkList data={data} page={page} teamId="team-a" />,
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamId/links',
	});
	const newLinkRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamId/links/new',
	});
	const editLinkRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamId/links/$linkId',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([linksRoute, newLinkRoute, editLinkRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe('LinkList', () => {
	/**
	 * The task brief warns this exact state is easy to get silently wrong: a
	 * list that renders nothing looks identical to a team with no links,
	 * unless the empty state actually says so. See the falsification note in
	 * the task report for the mutation this test exists to catch.
	 */
	it('shows the empty-state message when the team has no links', async () => {
		renderWith(pageOf());
		// `findBy*`, not `getBy*`, for the first assertion in every test here:
		// `RouterProvider`'s initial match resolves asynchronously (its own
		// microtask, separate from React's synchronous render), the same
		// reason `team-switcher.test.tsx` awaits its first query too.
		expect(await screen.findByText('No links yet.')).toBeInTheDocument();
		expect(screen.queryByRole('list')).not.toBeInTheDocument();
	});

	it('offers a link to create the first link when the team has none', async () => {
		// Finding 2: the empty state read as an actionable prompt ("Create your
		// first one") with nothing to click. Now it is an actual link.
		renderWith(pageOf());
		expect(await screen.findByRole('link', { name: 'Create link' })).toHaveAttribute(
			'href',
			'/teams/team-a/links/new',
		);
	});

	it('offers a link to create another link when the team already has links', async () => {
		// Finding 2, other half: a team that already has links must still be
		// able to reach the create page, not only a team with none.
		renderWith(pageOf({ items: [link()], total_count: 1 }));
		expect(await screen.findByRole('link', { name: 'Create link' })).toHaveAttribute(
			'href',
			'/teams/team-a/links/new',
		);
	});

	it('offers an edit link per row, addressed at both the team and the link', async () => {
		// Finding 2: rows had no edit link at all, so Task 11's entire edit and
		// delete surface was reachable only by hand-typing a URL containing a
		// UUID. The edit link interpolates *two* params — a wrong `teamId` or a
		// row's link confused with another's would both compile and pass a
		// role/name-only assertion, so this pins the actual `href` for each row.
		renderWith(
			pageOf({
				items: [link(), link({ id: 'link-2', short_url: 'https://short.invalid/def456' })],
				total_count: 2,
			}),
		);

		const editLinks = await screen.findAllByRole('link', { name: 'Edit' });
		expect(editLinks).toHaveLength(2);
		expect(editLinks[0]).toHaveAttribute('href', '/teams/team-a/links/link-1');
		expect(editLinks[1]).toHaveAttribute('href', '/teams/team-a/links/link-2');
	});

	it('lists every link on the page with a copy button and its destination', async () => {
		renderWith(
			pageOf({
				items: [link(), link({ id: 'link-2', short_url: 'https://short.invalid/def456' })],
				total_count: 2,
			}),
		);

		expect(
			await screen.findByRole('link', { name: 'https://short.invalid/abc123' }),
		).toBeInTheDocument();
		expect(screen.getAllByRole('listitem')).toHaveLength(2);
		expect(screen.getAllByRole('button', { name: 'Copy' })).toHaveLength(2);
	});

	it('shows the short-domain notice when the links live on an .invalid hostname', async () => {
		renderWith(pageOf({ items: [link({ hostname: 'short.invalid' })], total_count: 1 }));
		expect(await screen.findByRole('note')).toBeInTheDocument();
	});

	it('hides the short-domain notice once a real domain is configured', async () => {
		renderWith(pageOf({ items: [link({ hostname: 'kurze.url' })], total_count: 1 }));
		expect(await screen.findByRole('heading', { name: 'Your links' })).toBeInTheDocument();
		expect(screen.queryByRole('note')).not.toBeInTheDocument();
	});

	it('disables the previous-page control on the first page', async () => {
		renderWith(pageOf({ items: [link()], page: 1, per_page: 1, total_count: 2 }), 1);
		expect(await screen.findByRole('link', { name: 'Next page' })).toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Previous page' })).not.toBeInTheDocument();
	});

	it('disables the next-page control on the last page', async () => {
		renderWith(pageOf({ items: [link()], page: 2, per_page: 1, total_count: 2 }), 2);
		expect(await screen.findByRole('link', { name: 'Previous page' })).toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Next page' })).not.toBeInTheDocument();
	});
});
