import type { Link, LinkStats, StatBreakdown, StatDay } from '@kurze-url/api-client';
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import axe from 'axe-core';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { AuthedShell } from '../../components/authed-shell';
import { createI18n } from '../../i18n';
import type { StatsWindow } from '../../lib/stats-window';
import type { Membership } from '../_authed';
import { StatsPageBody } from './teams.$teamSlug.links.$linkId_.stats.tsx';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below traces to
   `@kurze-url/api-client`'s generated `Link`/`LinkStats`/`StatBreakdown`/`StatDay` types, whose
   nested arrays and properties are mutable — generated codegen output, never edited by hand. */

const memberships: Membership[] = [
	{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' },
];

const LINK: Link = {
	analytics_enabled: true,
	created_at: '2026-01-01T00:00:00.000Z',
	created_by: 'user-a',
	destination_url: 'https://example.org',
	domain_id: 'domain-a',
	expires_at: null,
	folder_id: 'folder-a',
	has_password: false,
	hostname: 'kurze.url',
	id: 'link-a',
	redirect_type: 302,
	short_url: 'https://kurze.url/abc123',
	slug: 'abc123',
	state: 'active',
	tags: [],
	team_id: 'team-a',
	updated_at: '2026-01-01T00:00:00.000Z',
};

const TOTALS_ZERO = { clicks: 0, human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 };
const EMPTY_BREAKDOWN: StatBreakdown = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: null,
};
const EMPTY_BREAKDOWNS = {
	bot_status: EMPTY_BREAKDOWN,
	browser: EMPTY_BREAKDOWN,
	country: EMPTY_BREAKDOWN,
	device: EMPTY_BREAKDOWN,
	os: EMPTY_BREAKDOWN,
	qr_vs_regular: EMPTY_BREAKDOWN,
	referrer: EMPTY_BREAKDOWN,
	utm_source: EMPTY_BREAKDOWN,
};

const SERIES: StatDay[] = [
	{
		clicks: 40,
		date: '2026-08-25',
		human_clicks: 32,
		human_unique_visitors: 28,
		unique_visitors: 30,
	},
	{ clicks: 0, date: '2026-08-26', human_clicks: 0, human_unique_visitors: 0, unique_visitors: 0 },
	{
		clicks: 55,
		date: '2026-08-27',
		human_clicks: 40,
		human_unique_visitors: 35,
		unique_visitors: 38,
	},
];

// A dimension whose top ten don't cover everything — carries an "other" row.
const BREAKDOWN_WITH_OTHER: StatBreakdown = {
	other_clicks: 12,
	other_unique_visitors: 9,
	other_values: 4,
	values: [
		{ clicks: 80, unique_visitors: 60, value: 'Chrome' },
		{ clicks: 20, unique_visitors: 15, value: 'Firefox' },
	],
};

// A dimension whose top ten is the whole picture — no "other" row.
const BREAKDOWN_WITHOUT_OTHER: StatBreakdown = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: [
		{ clicks: 60, unique_visitors: 40, value: 'macOS' },
		{ clicks: 40, unique_visitors: 30, value: 'Windows' },
	],
};

// The two dimensions the API only ever reports as a binary split.
const BOT_STATUS: StatBreakdown = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: [
		{ clicks: 750, unique_visitors: 700, value: 'human' },
		{ clicks: 250, unique_visitors: 200, value: 'bot' },
	],
};
const QR_VS_REGULAR: StatBreakdown = {
	other_clicks: 0,
	other_unique_visitors: 0,
	other_values: 0,
	values: [
		{ clicks: 300, unique_visitors: 280, value: 'qr' },
		{ clicks: 700, unique_visitors: 620, value: 'regular' },
	],
};

const DATA_STATS: LinkStats = {
	analytics_enabled: true,
	breakdowns: {
		bot_status: BOT_STATUS,
		browser: BREAKDOWN_WITH_OTHER,
		country: BREAKDOWN_WITHOUT_OTHER,
		device: BREAKDOWN_WITHOUT_OTHER,
		os: BREAKDOWN_WITHOUT_OTHER,
		qr_vs_regular: QR_VS_REGULAR,
		referrer: BREAKDOWN_WITH_OTHER,
		utm_source: BREAKDOWN_WITHOUT_OTHER,
	},
	from: '2026-08-25',
	link_id: 'link-a',
	series: SERIES,
	to: '2026-08-27',
	totals: { clicks: 1000, human_clicks: 750, human_unique_visitors: 700, unique_visitors: 900 },
};

const DISABLED_STATS: LinkStats = {
	analytics_enabled: false,
	breakdowns: EMPTY_BREAKDOWNS,
	from: '2026-08-25',
	link_id: 'link-a',
	series: [],
	to: '2026-08-27',
	totals: TOTALS_ZERO,
};

const EMPTY_STATS: LinkStats = {
	analytics_enabled: true,
	breakdowns: EMPTY_BREAKDOWNS,
	from: '2026-08-25',
	link_id: 'link-a',
	series: [],
	to: '2026-08-27',
	totals: TOTALS_ZERO,
};

/**
 * Renders the real `StatsPageBody` (exported from
 * `teams.$teamSlug.links.$linkId_.stats.tsx` for exactly this reason) inside
 * the real, composed `AuthedShell` — exactly how `_authed.tsx` nests every
 * route's content inside `SidebarInset`, whose own `<main>` is the page's
 * only landmark; the same reason `authed-shell.a11y.test.tsx` renders the
 * real shell rather than a bare fragment. Testing the page body on its own,
 * with no shell around it, would report a missing landmark for a reason
 * that has nothing to do with this page.
 *
 * Rendering `StatsPageBody` itself, rather than a local stand-in, is what
 * makes this suite react to a real composition change in the page: the
 * route's own `RouteComponent` renders this exact component too, so nothing
 * here can drift out from under what production actually ships.
 *
 * @param stats - The statistics document to render the page body for.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderComposedPage(stats: LinkStats): ReturnType<typeof render> {
	const rootRoute = createRootRoute({
		component: () => (
			<AuthedShell
				currentTeamSlug="verein-a"
				isMaintainer={false}
				memberships={memberships}
				onSignOut={vi.fn<() => void>()}
				signingOut={false}
				theme="light"
			>
				<StatsPageBody
					language="en"
					link={LINK}
					onWindowChange={vi.fn<(window: Readonly<StatsWindow>) => void>()}
					stats={stats}
					teamSlug="verein-a"
					today={new Date('2026-09-18T00:00:00Z')}
				/>
			</AuthedShell>
		),
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
	});
	const linkDetailRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links/$linkId',
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
		routeTree: rootRoute.addChildren([linksRoute, linkDetailRoute, domainsRoute, newTeamRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe('the statistics page', () => {
	// Finding: see `task-11-report.md` for whether axe reported anything real
	// here. `region` — a best-practice rule that only runs without
	// `.withTags()` — is what caught the design-system wave's only critical
	// defect (AuthedShell's own chrome sitting in no landmark), which is why
	// this suite never narrows the ruleset.
	it('has no axe violations under the default ruleset for the data state', async () => {
		renderComposedPage(DATA_STATS);
		await screen.findByRole('heading', { level: 1, name: 'https://kurze.url/abc123' });

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	it('has no axe violations under the default ruleset for the disabled state', async () => {
		renderComposedPage(DISABLED_STATS);
		await screen.findByText('Click counting is off for this link');

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	it('has no axe violations under the default ruleset for the empty state', async () => {
		renderComposedPage(EMPTY_STATS);
		await screen.findByText('No clicks in this window');

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});
});
