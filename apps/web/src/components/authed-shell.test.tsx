import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { Membership } from '../routes/_authed';
import { AuthedShell } from './authed-shell';

const memberships: Membership[] = [
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', slug: 'verein-b', team_id: 'b' },
];

/**
 * `AuthedShell` renders `TeamSwitcher`, which needs a router in context for
 * the same reason `team-switcher.test.tsx` gives for its own minimal,
 * test-only route tree.
 */
function renderShell(props: {
	readonly currentTeamSlug?: string;
	readonly isMaintainer?: boolean;
	readonly memberships?: readonly Membership[];
	readonly onSignOut?: () => void;
	readonly signingOut?: boolean;
}): ReturnType<typeof render> {
	const {
		currentTeamSlug = 'verein-a',
		isMaintainer = false,
		memberships: membershipsProp = memberships,
		onSignOut = vi.fn(),
		signingOut = false,
	} = props;

	const rootRoute = createRootRoute({
		component: () => (
			<AuthedShell
				currentTeamSlug={currentTeamSlug}
				isMaintainer={isMaintainer}
				memberships={membershipsProp}
				onSignOut={onSignOut}
				signingOut={signingOut}
			/>
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

describe('AuthedShell', () => {
	it('renders the team switcher, fed from the memberships prop', async () => {
		// Finding 2: `TeamSwitcher` was built, tested and storied but never
		// rendered anywhere in the actual app.
		renderShell({});
		expect(await screen.findByRole('navigation', { name: 'Teams' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Verein A' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Verein B' })).toBeInTheDocument();
	});

	it('offers a sign-out control that calls the caller-supplied handler', async () => {
		// Finding 2: `signOut` in `server/auth.ts` had no caller at all.
		const onSignOut = vi.fn();
		renderShell({ onSignOut });

		await userEvent.click(await screen.findByRole('button', { name: 'Sign out' }));
		expect(onSignOut).toHaveBeenCalledTimes(1);
	});

	it('disables the sign-out control while a sign-out is already in flight', async () => {
		renderShell({ signingOut: true });
		expect(await screen.findByRole('button', { name: 'Sign out' })).toBeDisabled();
	});

	it('offers team creation to a maintainer', async () => {
		// A maintainer who already belongs to a team never sees `/`'s own
		// "create team" link, because `/` redirects them straight into their
		// team. Without this control they would have no way to reach
		// `/new-team` from inside the app at all.
		renderShell({ isMaintainer: true });
		expect(await screen.findByRole('link', { name: 'Create team' })).toBeInTheDocument();
	});

	it('hides team creation from everyone else', async () => {
		// `POST /v1/teams` answers a non-maintainer with 403 and the route
		// itself 404s, so a visible control here would only ever be a dead end.
		renderShell({ isMaintainer: false });
		expect(await screen.findByRole('button', { name: 'Sign out' })).toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Create team' })).not.toBeInTheDocument();
	});

	it('links to both team pages', async () => {
		// Before this the shell had a team switcher and a sign-out control, so a
		// second team page was unreachable by clicking.
		renderShell({});
		expect(await screen.findByRole('link', { name: 'Links' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Domains' })).toBeInTheDocument();
	});

	it('omits the team-page navigation when there is no resolved current team', async () => {
		// Same condition as the team switcher: nothing to navigate between for
		// a visitor with zero memberships or a stale bookmark.
		renderShell({ currentTeamSlug: undefined, memberships: [] });
		expect(await screen.findByRole('button', { name: 'Sign out' })).toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Links' })).not.toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Domains' })).not.toBeInTheDocument();
	});

	it('omits the team switcher when there is no resolved current team', async () => {
		// A visitor with zero memberships (or a stale bookmark to a team they
		// have since left) can still reach this shell — `TeamSwitcher` has
		// nothing to switch between in that case.
		renderShell({ currentTeamSlug: undefined, memberships: [] });
		expect(await screen.findByRole('button', { name: 'Sign out' })).toBeInTheDocument();
		expect(screen.queryByRole('navigation', { name: 'Teams' })).not.toBeInTheDocument();
	});
});
