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
import { describe, expect, it } from 'vitest';

import { createI18n } from '../i18n';
import type { Membership } from '../routes/_authed';
import { TeamSwitcher } from './team-switcher';

const memberships: Membership[] = [
	{ name: 'Verein A', role: 'owner', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', team_id: 'b' },
];

/**
 * `TeamSwitcher` renders TanStack Router `<Link>` elements, which need a
 * router in context to resolve `to`/`params` into an `href` and to handle
 * clicks — unlike `LanguageSwitcher`, a plain `<button>`-based component
 * that needs no such context. A minimal, test-only route tree — just a root
 * that mounts the component and the one path it actually links to — is
 * enough; it doesn't need any of the real app's routes or loaders.
 */
function renderWith(currentTeamId: string): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => <TeamSwitcher currentTeamId={currentTeamId} memberships={memberships} />,
	});
	const linksRoute = createRoute({
		getParentRoute: () => rootRoute,
		path: '/teams/$teamId/links',
		component: () => null,
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([linksRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe('TeamSwitcher', () => {
	it('labels itself with the switcher name', async () => {
		renderWith('a');
		expect(await screen.findByRole('navigation', { name: 'Teams' })).toBeInTheDocument();
	});

	it('marks only the current team as the current page', async () => {
		renderWith('b');
		expect(await screen.findByRole('link', { name: 'Verein B' })).toHaveAttribute(
			'aria-current',
			'page',
		);
		expect(screen.getByRole('link', { name: 'Verein A' })).not.toHaveAttribute('aria-current');
	});

	it('writes the cookie for the clicked team, not the current one', async () => {
		renderWith('a');
		await userEvent.click(await screen.findByRole('link', { name: 'Verein B' }));
		expect(document.cookie).toContain('team=b');
	});
});
