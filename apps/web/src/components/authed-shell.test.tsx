import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider, useTranslation } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { Membership } from '../routes/_authed';
import { AuthedShell } from './authed-shell';

const memberships: Membership[] = [
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', slug: 'verein-b', team_id: 'b' },
];

/**
 * Stands in for a matched child route's own content. Renders an existing
 * catalogue string via `t()` rather than a literal — `react/jsx-no-literals`
 * is error-level project-wide, test files included — chosen for being unused
 * elsewhere in `AuthedShell`'s own rendered tree, so the assertion below
 * cannot match the wrong element.
 *
 * @returns A single paragraph, standing in for page content.
 */
function PageContent(): React.JSX.Element {
	const { t } = useTranslation();
	return <p>{t('footer.tagline')}</p>;
}

/**
 * `AuthedShell` renders `AppSidebar`, which renders `TeamSwitcher` — both
 * need a router in context for the same reason `team-switcher.test.tsx`
 * gives for its own minimal, test-only route tree.
 *
 * @param props - Partial overrides merged onto this fixture's own defaults before rendering `AuthedShell`.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderShell(props: {
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
			<AuthedShell
				currentTeamSlug={currentTeamSlug}
				isMaintainer={isMaintainer}
				memberships={membershipsProp}
				onSignOut={onSignOut}
				signingOut={signingOut}
				theme={theme}
			>
				<PageContent />
			</AuthedShell>
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

describe(AuthedShell, () => {
	it('renders the team switcher, fed from the memberships prop', async () => {
		// Finding 2: `TeamSwitcher` was built, tested and storied but never
		// rendered anywhere in the actual app. The trigger shows the current
		// team's name; opening it is what proves both memberships fed the list,
		// not just the current one.
		renderShell({});
		await userEvent.click(await screen.findByRole('button', { name: 'Verein A' }));
		await expect(screen.findByRole('menuitem', { name: 'Verein A' })).resolves.toBeInTheDocument();
		expect(screen.getByRole('menuitem', { name: 'Verein B' })).toBeInTheDocument();
	});

	it('offers a sign-out control that calls the caller-supplied handler', async () => {
		// Finding 2: `signOut` in `server/auth.ts` had no caller at all.
		const onSignOut = vi.fn<() => void>();
		renderShell({ onSignOut });

		await userEvent.click(await screen.findByRole('button', { name: 'Sign out' }));
		expect(onSignOut).toHaveBeenCalledOnce();
	});

	it('disables the sign-out control while a sign-out is already in flight', async () => {
		renderShell({ signingOut: true });
		await expect(screen.findByRole('button', { name: 'Sign out' })).resolves.toBeDisabled();
	});

	it('offers team creation to a maintainer', async () => {
		// A maintainer who already belongs to a team never sees `/`'s own
		// "create team" link, because `/` redirects them straight into their
		// team. Without this control they would have no way to reach
		// `/new-team` from inside the app at all.
		renderShell({ isMaintainer: true });
		await expect(screen.findByRole('link', { name: 'Create team' })).resolves.toBeInTheDocument();
	});

	it('hides team creation from everyone else', async () => {
		// `POST /v1/teams` answers a non-maintainer with 403 and the route
		// itself 404s, so a visible control here would only ever be a dead end.
		renderShell({ isMaintainer: false });
		await expect(screen.findByRole('button', { name: 'Sign out' })).resolves.toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Create team' })).not.toBeInTheDocument();
	});

	it('links to both team pages', async () => {
		// Before this the shell had a team switcher and a sign-out control, so a
		// second team page was unreachable by clicking.
		renderShell({});
		await expect(screen.findByRole('link', { name: 'Links' })).resolves.toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Domains' })).toBeInTheDocument();
	});

	it('omits the team-page navigation when there is no resolved current team', async () => {
		// Same condition as the team switcher: nothing to navigate between for
		// a visitor with zero memberships or a stale bookmark.
		renderShell({ currentTeamSlug: undefined, memberships: [] });
		await expect(screen.findByRole('button', { name: 'Sign out' })).resolves.toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Links' })).not.toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Domains' })).not.toBeInTheDocument();
	});

	it('omits the team switcher when there is no resolved current team', async () => {
		// A visitor with zero memberships (or a stale bookmark to a team they
		// have since left) can still reach this shell — `TeamSwitcher` has
		// nothing to switch between in that case.
		renderShell({ currentTeamSlug: undefined, memberships: [] });
		await expect(screen.findByRole('button', { name: 'Sign out' })).resolves.toBeInTheDocument();
		expect(screen.queryByRole('group', { name: 'Teams' })).not.toBeInTheDocument();
	});

	it('renders the matched child route inside the sidebar inset', async () => {
		// `SidebarInset` has to wrap the page content for the layout to work —
		// before this task, `AuthedShell` rendered only its own header and
		// `_authed.tsx` rendered `<Outlet>` as a sibling.
		renderShell({});
		await expect(
			screen.findByText('An open-source project for associations.'),
		).resolves.toBeInTheDocument();
	});

	it('labels the sidebar trigger for screen readers', async () => {
		renderShell({});
		await expect(
			screen.findByRole('button', { name: 'Toggle the navigation' }),
		).resolves.toBeInTheDocument();
	});
});
