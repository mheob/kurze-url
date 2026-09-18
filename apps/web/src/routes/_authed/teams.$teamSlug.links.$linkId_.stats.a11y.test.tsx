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

// Final-review finding: counting switched off does not mean the document is
// empty — this link collected the same 1000 clicks as `DATA_STATS`, then had
// analytics switched off afterwards, and every one of them is still in the
// response. The page must render them as ordinary data, with a banner, not
// the 'disabled' empty state `DISABLED_STATS` above exercises.
const DISABLED_WITH_HISTORY_STATS: LinkStats = {
	...DATA_STATS,
	analytics_enabled: false,
};

const RECORDED_ELSEWHERE = { from: '2026-06-12', to: '2026-07-03' };

const EMPTY_WITH_HISTORY_STATS: LinkStats = {
	...EMPTY_STATS,
	recorded: RECORDED_ELSEWHERE,
};

const DISABLED_WITH_ELSEWHERE_STATS: LinkStats = {
	...DISABLED_STATS,
	recorded: RECORDED_ELSEWHERE,
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
		// Residual finding 2: `EmptyTitle` renders a plain `<div>`, so this
		// state's own heading only exists at all because a real `<h2>` was
		// nested inside it — `findByRole` with `level: 2` is what proves that
		// heading survives, rather than merely waiting for its text via
		// `findByText`, which would pass just as well against a bare `<div>`.
		await screen.findByRole('heading', { level: 2, name: 'Click counting is off for this link' });

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	it('has no axe violations under the default ruleset for the empty state', async () => {
		renderComposedPage(EMPTY_STATS);
		// Same reason as the disabled state just above.
		await screen.findByRole('heading', { level: 2, name: 'No clicks in this window' });

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});

	// Residual finding 1: `totals.clicks` is window-scoped, so a link switched
	// off a month ago and still holding data in a wider window used to render
	// the same body copy as one that never recorded anything at all. `stats.
	// recorded` (Task 2) is what closes that gap now: `DISABLED_STATS` carries
	// no `recorded` key, which is what a link with no statistics anywhere
	// reports, so the plain body copy below is correct rather than a guess —
	// see the "empty views and the recorded range" suite below for the case
	// where `recorded` is present instead.
	it('shows the plain disabled body when there truly is nothing to point to', async () => {
		renderComposedPage(DISABLED_STATS);
		await screen.findByRole('heading', { level: 2, name: 'Click counting is off for this link' });

		expect(
			screen.getByText(
				'Nothing is recorded while it is off, so this is not the same as a link nobody clicked.',
			),
		).toBeInTheDocument();
	});

	it('renders historical data with a banner, not the disabled empty state, when counting is off but clicks remain', async () => {
		renderComposedPage(DISABLED_WITH_HISTORY_STATS);
		await screen.findByRole('heading', { level: 1, name: 'https://kurze.url/abc123' });

		// The banner is shown, the figures still render …
		expect(
			screen.getByText(
				'Click counting is currently switched off for this link — these figures are historical.',
			),
		).toBeInTheDocument();
		expect(screen.getByText('1,000')).toBeInTheDocument();
		// … and the 'disabled' empty state — the one for a link with no
		// history at all — must not also render.
		expect(screen.queryByText('Click counting is off for this link')).not.toBeInTheDocument();

		const results = await axe.run(document.body);
		expect(results.violations).toStrictEqual([]);
	});
});

describe('the empty views and the recorded range', () => {
	// Every test here awaits the state's own heading first, the same idiom
	// the suite above already uses (e.g. "has no axe violations … for the
	// disabled state"): `RouterProvider`'s initial match resolves
	// asynchronously, so a synchronous `getByRole`/`getByText` right after
	// `renderComposedPage` can run before anything has committed.
	it('offers the recorded window when counting is on and the window is empty', async () => {
		renderComposedPage(EMPTY_WITH_HISTORY_STATS);
		await screen.findByRole('heading', { level: 2, name: 'No clicks in this window' });

		expect(
			screen.getByRole('button', { name: 'Show Jun 12, 2026 – Jul 3, 2026' }),
		).toBeInTheDocument();
		expect(
			screen.getByText('This link has statistics outside the window you are looking at.'),
		).toBeInTheDocument();
	});

	it('offers it when counting is off and data was recorded before that', async () => {
		renderComposedPage(DISABLED_WITH_ELSEWHERE_STATS);
		await screen.findByRole('heading', { level: 2, name: 'Click counting is off for this link' });

		expect(
			screen.getByRole('button', { name: 'Show Jun 12, 2026 – Jul 3, 2026' }),
		).toBeInTheDocument();
		expect(
			screen.getByText(
				'Counting is off now, but statistics recorded before it was switched off are still here.',
			),
		).toBeInTheDocument();
	});

	it('says so plainly when there is nothing anywhere', async () => {
		renderComposedPage(EMPTY_STATS);
		await screen.findByRole('heading', { level: 2, name: 'No clicks in this window' });

		expect(screen.queryByRole('button', { name: /^Show /u })).not.toBeInTheDocument();
		expect(screen.getByText('Nothing has been recorded for this link.')).toBeInTheDocument();
	});

	it('leaves the disabled view without advice when there is nothing anywhere', async () => {
		renderComposedPage(DISABLED_STATS);
		await screen.findByRole('heading', { level: 2, name: 'Click counting is off for this link' });

		expect(screen.queryByRole('button', { name: /^Show /u })).not.toBeInTheDocument();
	});
});
