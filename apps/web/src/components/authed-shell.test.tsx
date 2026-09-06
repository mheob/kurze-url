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
	{ name: 'Verein A', role: 'owner', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', team_id: 'b' },
];

/**
 * `AuthedShell` renders `TeamSwitcher`, which needs a router in context for
 * the same reason `team-switcher.test.tsx` gives for its own minimal,
 * test-only route tree.
 */
function renderShell(props: {
	readonly currentTeamId?: string;
	readonly isMaintainer?: boolean;
	readonly memberships?: readonly Membership[];
	readonly onSignOut?: () => void;
	readonly signingOut?: boolean;
}): ReturnType<typeof render> {
	const {
		currentTeamId = 'a',
		isMaintainer = false,
		memberships: membershipsProp = memberships,
		onSignOut = vi.fn(),
		signingOut = false,
	} = props;

	const rootRoute = createRootRoute({
		component: () => (
			<AuthedShell
				currentTeamId={currentTeamId}
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
		path: '/teams/$teamId/links',
	});
	const newTeamRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/new',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([linksRoute, newTeamRoute]),
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
		// `/teams/new` from inside the app at all.
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

	it('omits the team switcher when there is no resolved current team', async () => {
		// A visitor with zero memberships (or a stale bookmark to a team they
		// have since left) can still reach this shell — `TeamSwitcher` has
		// nothing to switch between in that case.
		renderShell({ currentTeamId: undefined, memberships: [] });
		expect(await screen.findByRole('button', { name: 'Sign out' })).toBeInTheDocument();
		expect(screen.queryByRole('navigation', { name: 'Teams' })).not.toBeInTheDocument();
	});
});
