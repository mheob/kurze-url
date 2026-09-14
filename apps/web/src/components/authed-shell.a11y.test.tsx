import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import axe from 'axe-core';
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
 * Stands in for a matched child route's own content — the same fixture
 * `authed-shell.test.tsx`'s own `PageContent` is, and for the same reason:
 * `react/jsx-no-literals` is error-level project-wide, test files included,
 * so this renders an existing catalogue string via `t()` rather than a
 * literal.
 *
 * @returns A single paragraph, standing in for page content.
 */
function PageContent(): React.JSX.Element {
	const { t } = useTranslation();
	return <p>{t('footer.tagline')}</p>;
}

/**
 * Renders the real, composed `AuthedShell` — real `AppSidebar`,
 * `TeamSwitcher`, `LanguageSwitcher` and `ThemeToggle`, nothing mocked or
 * stubbed out — the same shape the design-system-and-shell review rendered
 * to find the sidebar's own chrome sitting in no landmark at all. A resolved
 * team plus `isMaintainer: true` puts every optional element (the team
 * switcher, the create-team link) on the page at once, the same combination
 * `authed-shell.test.tsx`'s own fixtures exercise separately.
 *
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderRealShell(): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => (
			<AuthedShell
				currentTeamSlug="verein-a"
				isMaintainer
				memberships={memberships}
				onSignOut={vi.fn<() => void>()}
				signingOut={false}
				theme="light"
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
	// Finding (Critical): before this branch's fix, the sidebar's team switcher,
	// language switcher, theme toggle, create-team link and sign-out button sat
	// in no landmark at all — only the section menu (`app-sidebar.tsx`'s own
	// `<nav>`) had one. `apps/web/e2e/links.spec.ts` and `domains.spec.ts` both
	// run `AxeBuilder` with no `.withTags()`, so this is what would have turned
	// those suites red; nothing else in this repository runs axe over a
	// composed page locally, which is what earns this test a permanent place
	// here rather than staying a throwaway check.
	it('has no axe violations under the default ruleset', async () => {
		renderRealShell();
		await screen.findByRole('button', { name: 'Sign out' });

		// `document.body`, not `document`: this test renders only `AuthedShell`,
		// not `__root.tsx`'s `<html lang>` wrapper, so scanning the whole
		// document would fail `html-has-lang` for a reason that has nothing to
		// do with this component — the same reason `e2e/links.spec.ts` and
		// `domains.spec.ts` never see that rule fire against the real, fully
		// server-rendered page.
		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});
});
