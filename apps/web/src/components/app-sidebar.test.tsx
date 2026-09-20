import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { Membership } from '../routes/_authed';
import { AppSidebar } from './app-sidebar';
import { SidebarProvider } from './ui/sidebar';

const memberships: Membership[] = [
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', slug: 'verein-b', team_id: 'b' },
];

/**
 * `AppSidebar` renders `TeamSwitcher`, which needs a router in context for
 * the same reason `team-switcher.test.tsx` gives for its own minimal,
 * test-only route tree — copied here rather than imported across test
 * files, per the same reasoning `authed-shell.test.tsx`'s own copy gives.
 *
 * @param props - Partial overrides merged onto this fixture's own defaults before rendering `AppSidebar`.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderSidebar(props: {
	readonly currentTeamSlug?: string;
	readonly isMaintainer?: boolean;
	readonly memberships?: readonly Membership[];
	readonly onSignOut?: () => void;
	readonly signingOut?: boolean;
	readonly theme?: 'dark' | 'light';
}): ReturnType<typeof render> {
	const {
		currentTeamSlug = 'verein-a',
		isMaintainer = false,
		memberships: membershipsProp = memberships,
		onSignOut = vi.fn<() => void>(),
		signingOut = false,
		theme = 'light',
	} = props;

	const rootRoute = createRootRoute({
		component: () => (
			<SidebarProvider>
				<AppSidebar
					currentTeamSlug={currentTeamSlug}
					isMaintainer={isMaintainer}
					memberships={membershipsProp}
					onSignOut={onSignOut}
					signingOut={signingOut}
					theme={theme}
				/>
			</SidebarProvider>
		),
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
	});
	const domainsRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/domains',
	});
	const newTeamRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/new-team',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([linksRoute, domainsRoute, newTeamRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe(AppSidebar, () => {
	// Every query below is `await screen.find...`, not the plain `getByRole` a
	// first draft of this suite used: `RouterProvider` resolves and commits its
	// first match asynchronously (`authed-shell.test.tsx` and
	// `team-switcher.test.tsx` already query this same test-only router this
	// way), so a synchronous query run in the same tick as `renderSidebar` can
	// see an empty tree. `findBy` and `queryBy` after an initial `await` are the
	// codebase's own way of waiting for that first commit without weakening
	// what is actually asserted.
	it('links to every section for the current team', async () => {
		renderSidebar({ currentTeamSlug: 'verein-a' });

		await expect(screen.findByRole('link', { name: 'Links' })).resolves.toHaveAttribute(
			'href',
			'/teams/verein-a/links',
		);
		expect(screen.getByRole('link', { name: 'Domains' })).toHaveAttribute(
			'href',
			'/teams/verein-a/domains',
		);
	});

	it('wraps the section links in a navigation landmark', async () => {
		// `SidebarMenu` (components/ui/sidebar.tsx) renders a plain `<ul>` — an
		// `aria-label` on a list is not a landmark, so the accessible name has to
		// live on a real `<nav>` wrapping it. This is what a screen reader's
		// quick-navigation region list actually depends on, not just the two
		// links' own presence.
		renderSidebar({ currentTeamSlug: 'verein-a' });

		await expect(
			screen.findByRole('navigation', { name: 'Sections' }),
		).resolves.toBeInTheDocument();
	});

	it('renders no section navigation without a resolved team', async () => {
		// A signed-in visitor with a stale bookmark to a team they have left reaches
		// this shell with no resolvable slug; the links would have nowhere to point.
		// This is the same guard `AuthedShell` documents today and it is carried over
		// unchanged.
		renderSidebar({ currentTeamSlug: undefined, memberships: [] });

		// The footer's theme control is unconditional, so awaiting it first is
		// what confirms the router has settled before the negative assertion
		// below runs synchronously.
		await screen.findByRole('button', { name: 'Switch to dark mode' });
		expect(screen.queryByRole('link', { name: 'Links' })).toBeNull();
	});

	it('offers a theme control, which the authenticated area never had', async () => {
		// `ThemeToggle` was only ever rendered by `SiteHeader` on the public pages,
		// so a signed-in visitor could not change theme at all. The sidebar footer
		// is where that gets fixed, and this pins it.
		renderSidebar({ theme: 'light' });

		await expect(
			screen.findByRole('button', { name: 'Switch to dark mode' }),
		).resolves.toBeInTheDocument();
	});
});

describe('the history entry', () => {
	const ADMIN: Membership[] = [{ name: 'Verein A', role: 'admin', slug: 'verein-a', team_id: 'a' }];
	const EDITOR: Membership[] = [
		{ name: 'Verein A', role: 'editor', slug: 'verein-a', team_id: 'a' },
	];

	it('offers the history to an admin', async () => {
		renderSidebar({ memberships: ADMIN });

		await expect(screen.findByRole('link', { name: 'History' })).resolves.toBeInTheDocument();
	});

	it('does not offer it to a member below admin', async () => {
		// A hidden entry is not a permission — the route refuses too (Task 7).
		// What this asserts is that the menu does not advertise a door the
		// reader cannot open, which is the difference between a product that
		// is restricted and one that looks broken.
		renderSidebar({ memberships: EDITOR });

		// `findByRole` for `Links` first settles the router before the negative
		// assertion runs synchronously, the same pattern the suite above uses.
		await screen.findByRole('link', { name: 'Links' });
		expect(screen.queryByRole('link', { name: 'History' })).not.toBeInTheDocument();
	});

	it('still offers links and domains to that member', async () => {
		// The gate must narrow one entry, not the menu. Without this, a
		// condition accidentally wrapping the whole `SidebarMenu` would pass
		// the test above while hiding the entire navigation.
		renderSidebar({ memberships: EDITOR });

		await expect(screen.findByRole('link', { name: 'Links' })).resolves.toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Domains' })).toBeInTheDocument();
	});
});

describe('the members entry', () => {
	it('shows the members link to a viewer, unlike the audit log', async () => {
		const VIEWER: Membership[] = [
			{ name: 'Verein A', role: 'viewer', slug: 'verein-a', team_id: 'a' },
		];
		renderSidebar({ memberships: VIEWER });

		await expect(screen.findByRole('link', { name: 'People' })).resolves.toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'History' })).not.toBeInTheDocument();
	});
});
