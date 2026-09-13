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
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
	{ name: 'Verein B', role: 'editor', slug: 'verein-b', team_id: 'b' },
];

/**
 * `TeamSwitcher` renders TanStack Router `<Link>` elements, which need a
 * router in context to resolve `to`/`params` into an `href` and to handle
 * clicks — unlike `LanguageSwitcher`, a plain `<button>`-based component
 * that needs no such context. A minimal, test-only route tree — just a root
 * that mounts the component and the one path it actually links to — is
 * enough; it doesn't need any of the real app's routes or loaders.
 *
 * @param currentTeamSlug - The slug of the team to render as current.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderWith(currentTeamSlug: string): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => <TeamSwitcher currentTeamSlug={currentTeamSlug} memberships={memberships} />,
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
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

describe(TeamSwitcher, () => {
	it('labels itself with the switcher name', async () => {
		renderWith('verein-a');
		await expect(screen.findByRole('group', { name: 'Teams' })).resolves.toBeInTheDocument();
	});

	it('shows the current team as the trigger label', async () => {
		// The items now live behind a trigger, rather than a flat list — this is
		// what a visitor sees before opening it, and what the other two tests
		// below open by clicking.
		renderWith('verein-b');
		await expect(screen.findByRole('button', { name: 'Verein B' })).resolves.toBeInTheDocument();
	});

	it('marks only the current team as the current page', async () => {
		// A menu item rendered through `render={<Link />}` keeps the anchor's
		// `href`, but the ARIA menu pattern's own `role="menuitem"` (assigned by
		// the base-ui primitive) takes precedence over the anchor's implicit
		// `link` role — so this queries by the role actually in the accessibility
		// tree, not the one the underlying element would have on its own.
		renderWith('verein-b');
		await userEvent.click(await screen.findByRole('button', { name: 'Verein B' }));

		await expect(screen.findByRole('menuitem', { name: 'Verein B' })).resolves.toHaveAttribute(
			'aria-current',
			'page',
		);
		expect(screen.getByRole('menuitem', { name: 'Verein A' })).not.toHaveAttribute('aria-current');
	});

	it('writes the cookie for the clicked team, not the current one', async () => {
		renderWith('verein-a');
		await userEvent.click(await screen.findByRole('button', { name: 'Verein A' }));
		await userEvent.click(await screen.findByRole('menuitem', { name: 'Verein B' }));
		expect(document.cookie).toContain('team=verein-b');
	});
});
